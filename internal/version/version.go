package version

// Set at build time via -ldflags:
//
//	go build -ldflags "-X github.com/adham90/opentrace/internal/version.Version=1.2.3
//	                    -X github.com/adham90/opentrace/internal/version.Commit=abc1234
//	                    -X github.com/adham90/opentrace/internal/version.Date=2025-06-15"
var (
	Version = "dev"
	Commit  = "unknown"
	Date    = "unknown"
)

func Full() string {
	if Version == "dev" {
		return "dev"
	}
	return Version + " (" + Commit + ") built " + Date
}
