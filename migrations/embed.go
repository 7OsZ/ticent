package migrations

import "embed"

// Files menyematkan seluruh file migrasi SQL ke dalam binary Go supaya aplikasi dapat dijalankan dari folder mana saja.
//
//go:embed *.sql
var Files embed.FS
