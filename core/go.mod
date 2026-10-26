module nightclip/core

go 1.27

require (
	github.com/coder/websocket v1.8.15
	github.com/skip2/go-qrcode v0.0.0-20200617195104-da1b6568686e
	github.com/tobischo/gokeepasslib/v3 v3.7.1
	golang.org/x/mobile v0.0.0-20261011034528-197770444867
	modernc.org/sqlite v1.61.0
)

require (
	github.com/dustin/go-humanize v1.1.0 // indirect
	github.com/google/uuid v1.6.0 // indirect
	github.com/mattn/go-isatty v0.0.24 // indirect
	github.com/ncruces/go-strftime v1.1.0 // indirect
	github.com/remyoudompheng/bigfft v0.0.0-20230129092748-24d4a6f8daec // indirect
	github.com/tobischo/argon2 v0.2.0 // indirect
	golang.org/x/crypto v0.58.0 // indirect
	golang.org/x/mod v0.42.0 // indirect
	golang.org/x/sync v0.24.0 // indirect
	golang.org/x/sys v0.49.0 // indirect
	golang.org/x/tools v0.52.0 // indirect
	modernc.org/libc v1.79.0 // indirect
	modernc.org/mathutil v1.7.1 // indirect
	modernc.org/memory v1.12.1 // indirect
)

replace github.com/tobischo/gokeepasslib/v3 => ./third_party/gokeepasslib
