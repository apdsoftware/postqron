module github.com/apdsoftware/postqron/services/api

go 1.26.0

require (
	github.com/apdsoftware/postqron/features/f03-auth v0.0.0
	github.com/apdsoftware/postqron/features/f04-workspaces v0.0.0
	github.com/apdsoftware/postqron/features/f05-social-connections v0.0.0
	github.com/apdsoftware/postqron/features/f06-composer v0.0.0
	github.com/apdsoftware/postqron/features/f10-entitlements v0.0.0
	github.com/apdsoftware/postqron/features/f12-account-privacy v0.0.0
	github.com/apdsoftware/postqron/features/f14-email v0.0.0
	github.com/apdsoftware/postqron/features/f26-cookie-consent-api v0.0.0
	github.com/apdsoftware/postqron/features/f30-app-shell v0.0.0
	github.com/apdsoftware/postqron/features/f31-admin-console v0.0.0
	github.com/apdsoftware/postqron/features/f34-prelaunch/api v0.0.0
	github.com/apdsoftware/postqron/packages/runtime v0.0.0
	github.com/jackc/pgx/v5 v5.9.2
)

require (
	github.com/apdsoftware/postqron/features/f15-operations v0.0.0 // indirect
	github.com/aws/aws-sdk-go-v2 v1.43.2 // indirect
	github.com/aws/aws-sdk-go-v2/aws/protocol/eventstream v1.7.15 // indirect
	github.com/aws/aws-sdk-go-v2/credentials v1.19.32 // indirect
	github.com/aws/aws-sdk-go-v2/internal/configsources v1.4.33 // indirect
	github.com/aws/aws-sdk-go-v2/internal/endpoints/v2 v2.7.33 // indirect
	github.com/aws/aws-sdk-go-v2/internal/v4a v1.4.34 // indirect
	github.com/aws/aws-sdk-go-v2/service/internal/accept-encoding v1.13.14 // indirect
	github.com/aws/aws-sdk-go-v2/service/internal/checksum v1.9.26 // indirect
	github.com/aws/aws-sdk-go-v2/service/internal/presigned-url v1.13.33 // indirect
	github.com/aws/aws-sdk-go-v2/service/internal/s3shared v1.19.34 // indirect
	github.com/aws/aws-sdk-go-v2/service/s3 v1.106.2 // indirect
	github.com/aws/smithy-go v1.27.5 // indirect
	github.com/coreos/go-oidc/v3 v3.17.0 // indirect
	github.com/go-jose/go-jose/v4 v4.1.4 // indirect
	github.com/jackc/pgpassfile v1.0.0 // indirect
	github.com/jackc/pgservicefile v0.0.0-20240606120523-5a60cdf6a761 // indirect
	github.com/jackc/puddle/v2 v2.2.2 // indirect
	golang.org/x/crypto v0.52.0 // indirect
	golang.org/x/oauth2 v0.31.0 // indirect
	golang.org/x/sync v0.21.0 // indirect
	golang.org/x/sys v0.45.0 // indirect
	golang.org/x/text v0.39.0 // indirect
	gopkg.in/yaml.v3 v3.0.1 // indirect
)

replace github.com/apdsoftware/postqron/packages/runtime => ../../packages/runtime/go

replace github.com/apdsoftware/postqron/features/f03-auth => ../../features/f03-auth

replace github.com/apdsoftware/postqron/features/f04-workspaces => ../../features/f04-workspaces

replace github.com/apdsoftware/postqron/features/f05-social-connections => ../../features/f05-social-connections

replace github.com/apdsoftware/postqron/features/f06-composer => ../../features/f06-composer

replace github.com/apdsoftware/postqron/features/f10-entitlements => ../../features/f10-entitlements

replace github.com/apdsoftware/postqron/features/f12-account-privacy => ../../features/f12-account-privacy

replace github.com/apdsoftware/postqron/features/f14-email => ../../features/f14-email

replace github.com/apdsoftware/postqron/features/f15-operations => ../../features/f15-operations

replace github.com/apdsoftware/postqron/features/f26-cookie-consent-api => ../../features/f26-cookie-consent-api

replace github.com/apdsoftware/postqron/features/f30-app-shell => ../../features/f30-app-shell

replace github.com/apdsoftware/postqron/features/f31-admin-console => ../../features/f31-admin-console

replace github.com/apdsoftware/postqron/features/f34-prelaunch/api => ../../features/f34-prelaunch/api
