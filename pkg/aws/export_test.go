package aws

// Test-only exports for the external aws_test package, whose tests import
// pkg/find (which imports this package, so they cannot live in package aws).
var LiveCatalog = liveCatalog
