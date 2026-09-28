// 最小 WebSocket 服务端（RFC 6455 握手 + 帧读写，纯标准库，不引第三方依赖）。
// 用途：实例状态变化时向控制台推送刷新事件，替代前端 15s 轮询。
package api

import (
	"crypto/sha1"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/linuxsuren/open-cloud-web/internal/auth"
)

const wsGUID = "258EAFA5-E914-47DA-95CA-C5AB0DC85B11"

// wsEvent 是推送事件负载（客户端收到后自行拉取最新列表）。
type wsEvent struct {
	Type      string `json:"type"`
	Timestamp int64  `json:"timestamp"`
}

// wsClient 一个已升级的连接：独立读写协程，send 缓冲写通道。
type wsClient struct {
	conn net.Conn
	send chan []byte
}

// Hub 管理全部在线连接并广播事件。
type Hub struct {
	mu      sync.Mutex
	clients map[*wsClient]struct{}
	tick    time.Duration // 周期广播间隔（兜底覆盖调度器侧的状态变化）
}

func NewHub(tick time.Duration) *Hub {
	return &Hub{clients: map[*wsClient]struct{}{}, tick: tick}
}

func (h *Hub) register(c *wsClient) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.clients[c] = struct{}{}
}

func (h *Hub) unregister(c *wsClient) {
	h.mu.Lock()
	defer h.mu.Unlock()
	delete(h.clients, c)
}

func (h *Hub) count() int {
	h.mu.Lock()
	defer h.mu.Unlock()
	return len(h.clients)
}

// Broadcast 向全部连接推送一条文本帧；慢消费者缓冲满则断开。
func (h *Hub) Broadcast(payload []byte) {
	h.mu.Lock()
	defer h.mu.Unlock()
	for c := range h.clients {
		select {
		case c.send <- payload:
		default:
			// 写不动的连接直接关闭，避免拖垮广播。
			close(c.send)
			delete(h.clients, c)
			_ = c.conn.Close()
		}
	}
}

// Run 启动周期广播（兜底：调度器在服务端进程内改状态不会走 API 层钩子）。
func (h *Hub) Run(stop <-chan struct{}) {
	if h.tick <= 0 {
		return
	}
	t := time.NewTicker(h.tick)
	defer t.Stop()
	for {
		select {
		case <-stop:
			return
		case <-t.C:
			h.Broadcast(refreshEvent())
		}
	}
}

func refreshEvent() []byte {
	b, _ := json.Marshal(wsEvent{Type: "instances.updated", Timestamp: time.Now().Unix()})
	return b
}

// wsUpgrade 完成 RFC 6455 握手（Sec-WebSocket-Accept）。
func wsUpgrade(w http.ResponseWriter, r *http.Request) (net.Conn, bool) {
	key := r.Header.Get("Sec-WebSocket-Key")
	if key == "" || strings.ToLower(r.Header.Get("Upgrade")) != "websocket" {
		http.Error(w, "expected websocket upgrade", http.StatusBadRequest)
		return nil, false
	}
	hj, ok := w.(http.Hijacker)
	if !ok {
		http.Error(w, "websocket unsupported", http.StatusInternalServerError)
		return nil, false
	}
	sum := sha1.Sum([]byte(key + wsGUID))
	accept := base64.StdEncoding.EncodeToString(sum[:])
	conn, _, err := hj.Hijack()
	if err != nil {
		return nil, false
	}
	resp := "HTTP/1.1 101 Switching Protocols\r\n" +
		"Upgrade: websocket\r\nConnection: Upgrade\r\n" +
		"Sec-WebSocket-Accept: " + accept + "\r\n\r\n"
	if _, err := conn.Write([]byte(resp)); err != nil {
		_ = conn.Close()
		return nil, false
	}
	return conn, true
}

// wsWriteFrame 写一个服务端帧（不掩码）：opcode 1=text 9=ping 10=pong。
func wsWriteFrame(w io.Writer, opcode byte, payload []byte) error {
	head := []byte{0x80 | opcode} // FIN=1
	n := len(payload)
	switch {
	case n < 126:
		head = append(head, byte(n))
	case n <= 0xFFFF:
		head = append(head, 126, byte(n>>8), byte(n))
	default:
		ext := make([]byte, 8)
		binary.BigEndian.PutUint64(ext, uint64(n))
		head = append(head, 127)
		head = append(head, ext...)
	}
	if _, err := w.Write(head); err != nil {
		return err
	}
	_, err := w.Write(payload)
	return err
}

// wsReadFrame 读一个客户端帧（必须掩码）：返回 opcode 与负载；出错返回非 nil。
// 只需识别 ping(9)/close(8)，其余负载丢弃。
func wsReadFrame(conn net.Conn) (byte, []byte, error) {
	var h [2]byte
	if _, err := io.ReadFull(conn, h[:]); err != nil {
		return 0, nil, err
	}
	opcode := h[0] & 0x0F
	masked := h[1]&0x80 != 0
	n := uint64(h[1] & 0x7F)
	switch n {
	case 126:
		var ext [2]byte
		if _, err := io.ReadFull(conn, ext[:]); err != nil {
			return 0, nil, err
		}
		n = uint64(ext[0])<<8 | uint64(ext[1])
	case 127:
		var ext [8]byte
		if _, err := io.ReadFull(conn, ext[:]); err != nil {
			return 0, nil, err
		}
		n = binary.BigEndian.Uint64(ext[:])
	}
	if n > 1<<20 {
		return 0, nil, io.ErrShortBuffer
	}
	var mask [4]byte
	if masked {
		if _, err := io.ReadFull(conn, mask[:]); err != nil {
			return 0, nil, err
		}
	}
	payload := make([]byte, n)
	if _, err := io.ReadFull(conn, payload); err != nil {
		return 0, nil, err
	}
	if masked {
		for i := range payload {
			payload[i] ^= mask[i%4]
		}
	}
	return opcode, payload, nil
}

// handleWS GET /api/v1/ws?token=<jwt|pat>：鉴权后维持推送连接。
// 浏览器 WebSocket 无法自定义 Authorization 头，token 走查询参数。
func (h *Handler) handleWS(w http.ResponseWriter, r *http.Request) {
	token := r.URL.Query().Get("token")
	u := h.wsAuth(token)
	if u == nil {
		http.Error(w, "invalid token", http.StatusUnauthorized)
		return
	}
	conn, ok := wsUpgrade(w, r)
	if !ok {
		return
	}
	c := &wsClient{conn: conn, send: make(chan []byte, 16)}
	h.Hub.register(c)
	// 立即推一条，客户端可借机刷新初值。
	select {
	case c.send <- refreshEvent():
	default:
	}
	go c.writePump()
	go c.readPump(h.Hub)
	h.audit(u.ID, "ws.connect", "")
}

// wsAuth 支持 JWT 与 PAT（与 Bearer 中间件同一套凭据体系）。
func (h *Handler) wsAuth(token string) *auth.User {
	if token == "" {
		return nil
	}
	if auth.IsPAT(token) {
		pat, err := h.Store.GetPATByHash(auth.HashToken(token))
		if err != nil || pat.Expired(h.t()) {
			return nil
		}
		return h.activeUser(pat.UserID)
	}
	claims, err := h.Auth.ParseJWT(token)
	if err != nil {
		return nil
	}
	return h.activeUser(claims.UID)
}

// activeUser 按 ID 取激活状态用户（禁用用户拒绝连接）。
func (h *Handler) activeUser(id int64) *auth.User {
	u, err := h.Store.GetUser(id)
	if err != nil || u.Status != auth.StatusActive {
		return nil
	}
	return u
}

func (c *wsClient) writePump() {
	ping := time.NewTicker(30 * time.Second)
	defer ping.Stop()
	for {
		select {
		case payload, ok := <-c.send:
			if !ok {
				_ = c.conn.Close()
				return
			}
			if err := wsWriteFrame(c.conn, 0x1, payload); err != nil {
				_ = c.conn.Close()
				return
			}
		case <-ping.C:
			if err := wsWriteFrame(c.conn, 0x9, []byte("ping")); err != nil {
				_ = c.conn.Close()
				return
			}
		}
	}
}

func (c *wsClient) readPump(hub *Hub) {
	defer func() {
		hub.unregister(c)
		_ = c.conn.Close()
	}()
	for {
		op, _, err := wsReadFrame(c.conn)
		if err != nil {
			return
		}
		switch op {
		case 0x8: // close
			_ = wsWriteFrame(c.conn, 0x8, nil)
			return
		case 0x9: // ping -> pong
			if wsWriteFrame(c.conn, 0xA, nil) != nil {
				return
			}
		}
	}
}
