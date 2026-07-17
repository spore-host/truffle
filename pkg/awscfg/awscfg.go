// Package awscfg loads an AWS SDK config for truffle through the shared
// spore.host config base (libs/sporeconfig), so truffle resolves the AWS
// profile and default region the same way every other tool does:
// flag > env (SPORE_*/AWS_*) > ~/.config/spore/config.toml > default.
//
// The CLI records its --profile flag value via SetFlags during
// PersistentPreRun (truffle's region handling is per-command via --regions, so
// only the profile and a shared default region flow through here). An unset
// profile/region means the ambient AWS chain, so truffle's behavior is unchanged
// unless the suite config is set.
package awscfg

import (
	"context"
	"sync"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/spore-host/libs/sporeconfig"
)

var (
	mu    sync.RWMutex
	flags sporeconfig.Flags
)

// SetFlags records the CLI flag values for shared-config resolution. Called once
// from the root command's PersistentPreRun. Empty fields fall through to
// env/file/default.
func SetFlags(profile, region string) {
	mu.Lock()
	defer mu.Unlock()
	flags = sporeconfig.Flags{Profile: profile, Region: region}
}

// Resolved returns the shared config (profile/region/account/output) using the
// recorded flags plus env/file/default. A malformed config file is tolerated.
func Resolved() sporeconfig.Config {
	mu.RLock()
	f := flags
	mu.RUnlock()
	cfg, _ := sporeconfig.Resolve(f)
	return cfg
}

// Load builds an aws.Config applying the shared profile and region.
//
// defaultRegion is a last-resort region used only if neither the shared config
// nor the ambient chain provides one (e.g. quota checks that must target a real
// region). Pass "" for no default. An empty resolved profile uses the ambient
// credential chain.
func Load(ctx context.Context, defaultRegion string) (aws.Config, error) {
	sc := Resolved()

	var opts []func(*awsconfig.LoadOptions) error
	if sc.Region != "" {
		opts = append(opts, awsconfig.WithRegion(sc.Region))
	} else if defaultRegion != "" {
		opts = append(opts, awsconfig.WithDefaultRegion(defaultRegion))
	}
	if sc.Profile != "" {
		opts = append(opts, awsconfig.WithSharedConfigProfile(sc.Profile))
	}
	return awsconfig.LoadDefaultConfig(ctx, opts...)
}
