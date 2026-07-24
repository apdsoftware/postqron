module github.com/apdsoftware/postqron/services/worker

go 1.26.0

require github.com/apdsoftware/postqron/packages/runtime v0.0.0

require (
	github.com/kr/text v0.2.0 // indirect
	gopkg.in/yaml.v3 v3.0.1 // indirect
)

replace github.com/apdsoftware/postqron/packages/runtime => ../../packages/runtime/go
