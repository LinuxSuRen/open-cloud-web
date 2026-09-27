// 云账号 Secret 加解密（转发 internal/secrets，见该包）。
package api

import "github.com/linuxsuren/open-cloud-web/internal/secrets"

var encryptSecret = secrets.Encrypt
var decryptSecret = secrets.Decrypt
