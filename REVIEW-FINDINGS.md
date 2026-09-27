# 交叉检验报告（agent-f）

> **修复状态（集成者注，commit ede1edb 及后续）**：S1–S6 全部严重项已修复（S1 读取-合并-写回；S2 vars 补 provider；S3 password_hash 落库 + POST /auth/login + 失败锁定；S4 JWT_SECRET 启动强校验；S5 main 注册双 provider；S6 stdout/stderr 分离 buffer）；建议项已修 A1（tfvars 数字/键名）、A3（销毁失败保持 Destroying 交调度器重试）、A6（CORS 接线）。`go vet` 与 `go test -race ./...` 全绿。A2/A4/A5/A7/A8 及 T 系列留作后续迭代（风险可控，报告保留供参考）。

审查范围：HEAD `8bc81c5`（集成统一镜像类型后）。只读审查 + `go vet` / `go test -race` 全量验证 + 临时测试程序，未修改任何业务代码。

> 说明：原计划联网核对 alicloud/volcengine provider 文档，web 搜索因配额不足不可用；模板字段按模型知识核对，见 T8。

## 统计

| 级别 | 数量 |
| --- | --- |
| 严重（必须修） | 6 |
| 建议（应修） | 8 |
| 提示（可选） | 8 |

---

## 严重（必须修）

### S1. scheduler 回写部分字段导致 UpdateInstance 清空整行数据（数据丢失 + 续用限制失效）

- 位置：`cmd/ocw/main.go:213-224`（storeSchedulerAdapter.UpdateInstance）+ `internal/store/sqlite.go:510-517`
- 问题：`SQLiteStore.UpdateInstance` 是**全列覆盖 UPDATE**（name/provider/region/zone/image_id/instance_type/duration_sec/public_ip/private_ip/renewed_at 等全部重写）。而适配器从 `scheduler.Instance`（最小视图，只有 ID/UserID/Status/TfWorkspace/ErrorMessage/ExpiresAt/CreatedAt/UpdatedAt）构造 `model.Instance` 回写，未拷贝的字段全部为零值。
- 影响：调度器每处理一个实例（过期置 Destroying、销毁成功置 Destroyed、Creating 超时置 Failed、失败重试记录 ErrorMessage）都会把该实例的 `Name=""、Provider=""、Region/Zone/ImageID/InstanceType=""、PublicIP/PrivateIP=""、DurationSec=0、RenewedAt=nil` 整体抹掉。最危险的连锁：**RenewedAt 被清空后 `CanRenew` 重新为 true，用户可无限次续用**，"最多续用一次"的需求被破坏；同时 destroy 依赖的展示/审计信息全丢。
- 建议：修 `storeSchedulerAdapter.UpdateInstance`：先 `GetInstance(i.ID)` 取完整行，仅覆盖 Status/ErrorMessage/UpdatedAt（必要时 ExpiresAt）后回写；更彻底的做法是给 store 增加按字段的条件更新（`UPDATE ... SET status=? ... WHERE id=? AND status=?`，顺带解决 A2 的竞态）。

### S2. API 调 tofu Apply 时缺少 provider 变量，实例创建 100% 失败

- 位置：`internal/api/instances.go:133-140`（applyInstance 的 vars）vs `internal/tofu/runner.go:109-112`（`vars["provider"]` 为空直接报错）
- 问题：`runner.Apply` 用 `vars["provider"]` 选择模板与云凭证，缺失即返回错误"vars 缺少 provider 字段"；而 `applyInstance` 只传了 region/zone/image_id/instance_type/instance_name/bandwidth。
- 影响：`POST /instances` 虽返回 202，但后台 apply 必然失败，实例全部转 Failed。核心功能（OpenTofu 创建云主机）整体不可用。
- 建议：vars 增加 `"provider": inst.Provider`；并在 api 测试的 fakeRunner 中断言关键 vars（当前 fake 完全忽略参数，所以这个断裂测不出来，见"测试质量"）。

### S3. 密码体系断裂：哈希从未落库，且无密码登录端点（疑点 4 的结论）

- 位置：`internal/api/users.go:93-102`、`cmd/ocw/main.go:166-177`、`internal/store/sqlite.go`（未实现 `SetUserPasswordHash`）
- 问题：`bootstrapAdmin` 和 `createUser` 都通过可选能力接口 `PasswordHashSetter` 保存 bcrypt 哈希，但 **SQLiteStore 没有实现该方法**（仓库内只有 `internal/api/fakes_test.go:107` 的测试 fake 实现了它）——类型断言失败，哈希被静默丢弃，`storePasswordHash` 连错误都不返回。同时全代码不存在任何用户名/密码登录端点（routes 里只有 feishu OAuth、PAT 换 JWT、PAT 管理）。
- 结论（疑点 4）：**非飞书环境下第一个 admin 无任何认证路径**。`OCW_ADMIN_BOOTSTRAP_TOKEN` 引导创建的 admin 用户既没有可用的密码（哈希没存上），也没有能兑换成 JWT 的 PAT（PAT 创建需要先认证），`/auth/token` 只接受库里已存在的 PAT。
- 建议方案（任选其一，推荐 a）：
  a. 让 `OCW_ADMIN_BOOTSTRAP_TOKEN` 本身就是一个 PAT：bootstrap 时生成 `pat_ocw_...` 并把哈希写入 pats 表，启动日志/一次性文件输出明文，admin 用它走 `/auth/token` 换 JWT；
  b. 实现 `POST /auth/login`（username+password，bcrypt 校验，限速），并给 SQLiteStore 增加 password_hash 列与 `SetUserPasswordHash`。
  同时把 `storePasswordHash` 的静默失败改为返回 500——静默 optional-ability 是本次断裂没被发现的根因。

### S4. JWT_SECRET 不做任何校验，空密钥即可伪造 admin JWT

- 位置：`internal/config/config.go:57`（仅读 env，无非空/长度校验）、`internal/auth/jwt.go:34-37`（注释"至少 16 字节"但不 enforcement）、`cmd/ocw/main.go:57`
- 问题：默认值为空字符串，服务照常启动。空 secret 签名的 HS256 JWT 任何人都能离线伪造（`uid=1, role=admin`），配合 `loadActiveUser` 查库即通过认证。
- 影响：完全的认证绕过。README/deployment.md 都标注"必填"，但代码不强制——文档与实现不一致，且不一致的方向正好是安全漏洞。
- 建议：`config.Load()` 校验 `JWTSecret` 非空且 ≥32 字节（或至少 16），否则返回启动错误；`NewManager` 同步加防御。

### S5. 云 provider 从未注册，目录查询端点运行时全部 404

- 位置：`cmd/ocw/main.go:65-66`（注释"就位后在此 Register"，但没有下文）
- 问题：`internal/cloud` 提供了 `Register/Get` 全局注册表与 alicloud/volcengine 两个实现，但 main 从未调用 `cloud.Register(cloud.NewAlicloudProvider(...))` 等。`cloudRegistryAdapter.Provider` → `cloud.Get` 永远返回"未注册的 provider"。
- 影响：`GET /providers/{p}/regions|images|instance-types` 全部 404，需求"镜像/规格/区域动态查询而非写死"运行时不成立（代码本身实现了，是装配遗漏）。
- 建议：main 中按 cfg 的 AK/SK 注册两个 provider（AK 为空时可记 warning 仍注册，或跳过并文档化）。

### S6. tofu runner.run 存在真实数据竞争（`go test -race` 失败）

- 位置：`internal/tofu/runner.go:352-362`：`combined` 这一个 `bytes.Buffer` 同时被赋给 `cmd.Stdout` 和 `cmd.Stderr`
- 问题：`exec` 对 stdout/stderr 各起一个 goroutine 并发拷贝，写同一个非线程安全的 buffer。`go test -race ./internal/tofu/` 稳定复现（TestApplySuccessAndOutput）。
- 影响：init/destroy/output 每次调用都有竞争，输出可能交错损坏（错误信息被截断/污染）；race detector 下测试失败，阻塞 CI。
- 建议：stdout 与 stderr 各用一个 buffer（或用带锁的 writer），结束后拼接。

---

## 建议（应修）

### A1. tfvars 键名不匹配：`bandwidth` vs `public_bandwidth`

- 位置：`internal/api/instances.go:139`（写 `"bandwidth": "5"`）vs `internal/tofu/templates/*/variables.tf`（声明的是 `public_bandwidth`，number 类型）
- 影响：未声明变量在 tfvars 中会被 tofu 忽略（告警），带宽永远是模板默认 5 Mbps；即便改成同名，`writeTFVars` 只写字符串，number 变量需要非字符串 JSON 值。
- 建议：API 传 `public_bandwidth`；`writeTFVars` 支持 number 值（map[string]any 或在模板侧全部用 string + tonumber()）。

### A2. renew/delete 与 scheduler 的读-判-写竞态（无状态守卫）

- 位置：`internal/api/instances.go:249-284`（renew：loadOwnedInstance→CanRenew→UpdateInstance）、`287-307`（delete 同型）
- 问题：检查与写入之间无原子性。例如 scheduler 刚把过期实例置 Destroying 并发起了 destroy，renew 的全列 UPDATE 会把 Status 写回 Running（内存里读到的是旧值）；随后在途 destroy 完成又写 Destroyed——状态机被踩踏，审计与用户视图混乱。ExpiresAt 也可能被改回未来，语义上"已开始销毁的实例又被续用"。
- 建议：UpdateInstance 加 `WHERE status=?` 乐观条件（影响行数=0 视为冲突返回 409/400），或 store 提供 `RenewInstance(id, fromStatus, ...)` 专用原子方法。

### A3. API 提前销毁失败即置 Failed，退出调度器重试 → 云资源泄漏路径

- 位置：`internal/api/instances.go:309-315`（destroyInstance 失败调 markFailed）
- 问题：scheduler 只扫 Creating/Running/Destroying；Failed 不再被重试。API 销毁失败（网络抖动、凭证临时问题）后实例停在 Failed，云上资源无人回收，直到用户再手动 DELETE 一次（用户未必知道要重试）。
- 建议：API 销毁失败保持 Destroying 并记 ErrorMessage，交给 scheduler 退避重试（它就是为此设计的）。

### A4. applyInstance 复用同一 ctx 做 OutputIP；select 双就绪可能误杀成功的 apply

- 位置：`internal/api/instances.go:141-157`
- 问题：apply 用 30min ctx；若 apply 在超时边缘成功返回，`select` 可能仍选中 `ctx.Done()` 分支判为失败；即使走到 OutputIP，ctx 已过期会立刻失败。两种情况都会把实际已创建资源的实例置 Failed，且 Failed 不触发回收（同 A3 的泄漏面）。
- 建议：OutputIP 用独立的短超时 ctx；select 只挂 `done`，超时场景用 `ctx.Err()` 兜底区分。

### A5. OAuth state 可无限重放、无 TTL

- 位置：`internal/auth/state.go:32-40`
- 问题：state 只做 HMAC 校验，不消费、不记已用集合、无过期时间。拿到一个合法 state（例如从浏览器历史/日志）可以在任意时间重放完成回调。配合飞书 code 本身一次性，风险有限，但防 CSRF 强度低于预期。
- 建议：Validate 后消费（内存 TTL set，或 HMAC 里编入时间戳并限 10min 窗口）。

### A6. CORS 是死代码：配置项从未接线

- 位置：`internal/api/handler.go:20,141-157`（CORSAllowedOrigin）vs `cmd/ocw/main.go:105-108`（构造 api.Config 不设置该字段）、`internal/config/config.go`（无对应环境变量）
- 影响：CORS 中间件永远不会加头；`docs/api.md` 第 8 行宣称"CORS 由服务端配置（环境变量）"与实际不符。
- 建议：config 增加 `CORS_ALLOWED_ORIGIN` 并传入 api.Config；或删除文档承诺。

### A7. Creating 超时置 Failed 不回收资源；在途 apply 完成后直接放弃

- 位置：`internal/scheduler/scheduler.go:190-203`、`internal/api/instances.go:159-166`
- 问题：15min 超时置 Failed，但此时 applyInstance goroutine 可能还在跑（上限 30min）。它完成后发现 `fresh.Status != StatusCreating` 就直接 return——若 tofu 实际把资源建起来了，这些资源永远无人销毁。
- 建议：applyInstance 在发现状态已非 Creating 时（被超时置 Failed），对已完成的 workspace 发起 destroy 兜底；或 scheduler 对 Creating 超时也走 destroy 流程后再置 Failed。

### A8. 删除用户级联删除实例记录但不销毁云资源

- 位置：`internal/api/users.go:173-189` + `internal/store/sqlite.go`（instances 外键 ON DELETE CASCADE）
- 影响：admin 删除仍有 Running 实例的用户后，DB 记录消失，scheduler 再也看不到这些实例，云上资源持续计费。
- 建议：DeleteUser 前检查并销毁该用户全部非终态实例（或拒绝删除）。

---

## 提示（可选）

- **T1** `TOFU_TEMPLATES_DIR` 是死配置：runner 用 `go:embed` 内嵌模板（`internal/tofu/runner.go:38-39,80-82`），`Config.TofuTemplatesDir` 没有任何消费者；compose/Dockerfile 挂载/构建 `/app/templates` 属无效动作，`deploy/README.md` 相关描述与实现不符。建议删配置与挂载，或让 runner 支持从磁盘目录读模板。
- **T2** `model.CanTransition` 状态机（`internal/model/instance.go:26-57`）定义完整但全仓库无调用——store.UpdateInstance 不校验转换合法性。建议在 store 层接入，可顺带缓解 A2。
- **T3** PAT `expiresInDays<=0` 即永不过期（`internal/api/auth_endpoints.go:137-140`），建议文档明示并在管理端提供强制过期策略。
- **T4** `deleteInstance` 只允许 Running/Failed 提前销毁（`instances.go:292`），Creating 中的实例用户无法取消，只能等 15min 超时；状态机本身允许 Creating→Destroying。
- **T5** admin 的 `GET /instances` 通过遍历 ListUsers 再逐用户查（`instances.go:186-204`），N+1 查询；建议 store 增加 ListAllInstances。
- **T6** `requestIDLog` 在 crypto/rand 失败时直接 500（`handler.go:122-126`），实际概率可忽略，可降级为固定前缀。
- **T7** `detectProvider` 失败时 `env("")` 不带任何凭证执行 destroy（`runner.go:151-153,211-221`），报错信息会令人困惑；可在 workspace 未初始化时给出明确错误。
- **T8** 模板审查（联网验证不可用，基于模型知识）：alicloud 侧 `internet_max_bandwidth_out` / `public_ip` / `private_ip` / `security_groups` 用法正确；volcengine 侧 `volcengine_ecs_instance` 的 `instance_type_id/subnet_id/security_group_ids/instance_charge_type="PostPaid"`、EIP `billing_type="PostPaidByTraffic"`、`primary_ip`（已用 try 兜底）与本人对该 provider 的知识一致，未发现明显字段错误。建议 CI 增加 `tofu init/validate` 冒烟以固化验证。

---

## 需求符合性核查

| 需求 | 结论 | 依据 |
| --- | --- | --- |
| OpenTofu 创建阿里云+火山引擎云主机 | ✗（代码在，运行不可用） | S2：apply 必然失败；S6：race |
| 镜像/规格/区域动态查询而非写死 | ✗（实现完整但装配遗漏） | S5：provider 未注册，端点全 404 |
| 默认 1 小时有效期 | ✅ | config 默认 3600s，`ExpiresAt=apply完成+duration` |
| 到期前可手动续用一次 | 部分 ✗ | 逻辑在（CanRenew），但 S1 清空 RenewedAt 后退化为可无限续用；A2 有竞态 |
| 超时自动销毁 | ✅（有瑕疵） | scheduler 30s 扫描+退避重试正确；S1 抹字段、A3/A7/A8 泄漏路径 |
| 用户管理 + 飞书 OAuth + PAT | 部分 | 飞书 OAuth/PAT 实现良好；本地密码体系断裂（S3），非飞书环境无法引导 admin |
| 管理员可设置每用户单次最长时长 | ✅ | PATCH maxDurationSec + durationCap=min(用户,全局) |
| 容器化部署 | ✅（小瑕疵） | 多阶段 Dockerfile/非 root/HEALTHCHECK/entrypoint 齐备；T1 模板挂载无效 |

## 安全专项小结

- SQL 注入：✅ 全部参数化（ListInstancesByStatuses 占位符自建）。
- 路径穿越：✅ TfWorkspace 由服务端生成且 runner 校验 `validWorkspace`。
- 凭据：✅ 云 AK/SK 仅经 TF_VAR_ 环境变量注入，不落盘（tfvars 只含非敏感字段，0600）；PAT 只存 SHA-256，明文一次性返回；日志无凭据泄漏。
- CSRF state：部分（A5 可重放）。
- 越权：✅ 实例所有权校验（admin 放行）、admin 端点双重中间件、PAT 只能删自己的。
- CORS：见 A6（死代码）。
- 认证绕过：✗ S4（空 JWT secret）。
- SSRF：无用户可控 URL（云端点/飞书端点均硬编码）。

## 测试质量与全量验证

- `go vet ./...` ✅ 通过。
- `go test -race ./...` ✗ **FAIL**：`internal/tofu`（S6 数据竞争）；其余包（api/store/scheduler/auth/config/model/cloud）通过。
- 断言质量抽查：api 测试的 `fakeRunner.Apply` 完全忽略 vars 参数（`fakes_test.go:243`），导致 S2 这种跨包契约断裂无法被测出；tofu 测试则总是自己传 `provider`。两侧各自 green、集成即碎——建议 fakeRunner 记录并断言 vars（至少 provider/bandwidth 键名与模板变量一致）。
- scheduler 测试覆盖了过期销毁、退避、Creating 超时，质量较好；但对"回写字段完整性"无测试（正是 S1 溜过去的原因），装配层（cmd/ocw）整体零测试。

## 文档一致性

- README 环境变量表与 `internal/config` 一致（裸名列出 + 顶部说明 `OCW_` 前缀优先，正确）。
- 不一致点：README/docs 标 `JWT_SECRET`"必填"但代码不校验（S4）；`docs/deployment.md:115` 称"启动报 JWT_SECRET 相关错误"是虚构行为（不会发生）；`docs/api.md` 的 CORS 描述（A6）；`TOFU_TEMPLATES_DIR` 文档有、代码无消费者（T1）。

## 总体结论

| 维度 | 评价 |
| --- | --- |
| 架构与分层 | 良好：契约清晰、接口隔离合理、镜像类型统一为别名的做法正确 |
| 核心功能可用性 | **不合格**：S2+S5 使"创建云主机"与"动态查询"两条主链路运行时全断 |
| 安全 | 有一个致命项（S4）+ 一个体系断裂（S3）；其余安全面做得相当规范 |
| 并发正确性 | 弱：S1/S6/A2/A3/A4 共五处，集中在"全列覆盖 UPDATE + 无状态守卫"这个共性设计缺陷上 |
| 测试 | 单包质量尚可，但 fake 契约太松 + 装配层零覆盖，恰是全部严重问题的盲区 |

修复优先级建议：S4（一行校验，先堵认证绕过）→ S2/S5（装配补齐，恢复主链路）→ S3（bootstrap admin 认证路径）→ S1+A2（改为条件更新，一并解决竞态）→ S6 → 其余建议项。
