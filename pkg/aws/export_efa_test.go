package aws

import (
	"context"
	"regexp"

	"github.com/aws/aws-sdk-go-v2/aws"
)

// SearchFixture runs the search's per-region matching (server-side filters
// included) over fixtureCatalog, for the external aws_test package, whose
// tests import pkg/find and so cannot live in package aws.
func SearchFixture(matcher *regexp.Regexp, opts FilterOptions) ([]string, error) {
	fake := &fakeEC2{catalog: fixtureCatalog()}
	matched, err := matchingInstanceTypes(context.Background(), fake.describe, matcher, opts)
	if err != nil {
		return nil, err
	}
	names := make([]string, 0, len(matched))
	for _, it := range matched {
		names = append(names, string(it.InstanceType))
	}
	return names, nil
}

// FixtureEFATypes returns the fixture types whose EC2 record reports EFA
// support, read from the records rather than listed.
func FixtureEFATypes() []string {
	var out []string
	for _, it := range fixtureCatalog() {
		if it.NetworkInfo != nil && aws.ToBool(it.NetworkInfo.EfaSupported) {
			out = append(out, string(it.InstanceType))
		}
	}
	return out
}
