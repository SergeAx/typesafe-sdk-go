package typesafe

import (
	"fmt"
	"os"
	"runtime"
	"strings"
	"time"
)

// Version is the SDK version, reported in the User-Agent and X-TypeSafe-SDK headers.
const Version = "0.1.0"

// Environment variables read when the matching option is not set. Empty or
// whitespace-only values are ignored.
const (
	APIKeyEnv       = "TYPESAFE_API_KEY"
	BaseURLEnv      = "TYPESAFE_BASE_URL"
	DefaultModelEnv = "TYPESAFE_DEFAULT_MODEL"
	LogLevelEnv     = "TYPESAFE_LOG_LEVEL"
)

// SDK defaults, used when neither an option nor an environment variable applies.
const (
	DefaultBaseURL = "https://api.typesafe.ai"
	DefaultModel   = "jev-latest"
	DefaultTimeout = 10 * time.Second
)

const (
	sdkName       = "typesafe-sdk"
	systemOnePath = "/v1/systemone"
	modelsPath    = "/v1/models"

	jsonContentType = "application/json"

	requestIDHeader  = "X-Typesafe-Request-Id"
	retryCountHeader = "X-Typesafe-Retry-Count"
	sdkHeader        = "X-Typesafe-Sdk"
	runtimeHeader    = "X-Typesafe-Runtime"
)

// runtimeDescription identifies the Go toolchain and platform to the API.
var runtimeDescription = fmt.Sprintf("go/%s (%s; %s)",
	strings.TrimPrefix(runtime.Version(), "go"), runtime.GOOS, runtime.GOARCH)

func env(name string) string {
	return strings.TrimSpace(os.Getenv(name))
}
