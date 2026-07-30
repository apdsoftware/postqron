package publishingruntime

import (
	"fmt"
	"strings"
)

// FailClosedDynamicBootstrap is the real worker composition decision for the
// current stacked base. The concrete F5 dynamic adapters from #314/#328 are
// not integrated here, so an operator request to enable either provider must
// stop startup rather than create a partial executor or media stub.
func FailClosedDynamicBootstrap(
	mastodonEnabled, blueskyEnabled string,
) (DynamicAdapterDependencies, error) {
	mastodon, err := strictBool("POSTQRON_F08_MASTODON_ENABLED", mastodonEnabled)
	if err != nil {
		return DynamicAdapterDependencies{}, err
	}
	bluesky, err := strictBool("POSTQRON_F08_BLUESKY_ENABLED", blueskyEnabled)
	if err != nil {
		return DynamicAdapterDependencies{}, err
	}
	if mastodon || bluesky {
		return DynamicAdapterDependencies{}, fmt.Errorf(
			"dynamic publishing is unavailable: F5 dependencies #314/#328 are not integrated on integration/329-publishing-base",
		)
	}
	return DynamicAdapterDependencies{}, nil
}

func strictBool(name, raw string) (bool, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return false, nil
	}
	switch raw {
	case "true":
		return true, nil
	case "false":
		return false, nil
	default:
		return false, fmt.Errorf("%s must be true or false", name)
	}
}
