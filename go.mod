module github.com/gezihua123/phonefast

go 1.26.4

require (
	github.com/asticode/go-astiav v0.40.0
	github.com/gezihua123/phonefast/ocr v0.0.0-00010101000000-000000000000
	github.com/mark3labs/mcp-go v0.54.1
)

require (
	github.com/asticode/go-astikit v0.42.0 // indirect
	github.com/ebitengine/purego v0.9.0 // indirect
	github.com/google/jsonschema-go v0.4.2 // indirect
	github.com/google/uuid v1.6.0 // indirect
	github.com/santhosh-tekuri/jsonschema/v6 v6.0.2 // indirect
	github.com/shota3506/onnxruntime-purego v0.0.0-20260315223538-8db8bd7424b2 // indirect
	github.com/spf13/cast v1.7.1 // indirect
	github.com/yosida95/uritemplate/v3 v3.0.2 // indirect
	golang.org/x/text v0.14.0 // indirect
)

// OCR 模块拆分为独立 module（go.work 仅本地开发用，被 gitignore）。
// replace 让克隆/CI 在无 go.work 时也能解析 github.com/gezihua123/phonefast/ocr -> ./ocr。
replace github.com/gezihua123/phonefast/ocr => ./ocr
