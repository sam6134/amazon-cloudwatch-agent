// Copyright The OpenTelemetry Authors
// SPDX-License-Identifier: Apache-2.0

package deltatosparseprocessor // import "github.com/open-telemetry/opentelemetry-collector-contrib/processor/deltatosparseprocessor"

import (
	"go.opentelemetry.io/collector/component"
)

type Config struct {
	// Include specifies a filter on the metrics that should be converted.
	Include []string `mapstructure:"include"`
}

// Verify Config implements Processor interface.
var _ component.Config = (*Config)(nil)

// Validate does not check for unsupported dimension key-value pairs, because those
// get silently dropped and ignored during translation.
func (config *Config) Validate() error {
	return nil
}
