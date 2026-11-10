// Paket doğrulamada kullanılan şema dosyalarını gömer.
package schemas

import "embed"

//go:embed *.schema.json
var FS embed.FS
