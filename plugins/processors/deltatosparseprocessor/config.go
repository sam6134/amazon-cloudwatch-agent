// Copyright The OpenTelemetry Authors
// SPDX-License-Identifier: Apache-2.0

package deltatosparseprocessor // import "github.com/open-telemetry/opentelemetry-collector-contrib/processor/deltatosparseprocessor"

import (
	"fmt"

	"go.opentelemetry.io/collector/component"

	"github.com/open-telemetry/opentelemetry-collector-contrib/internal/filter/filterset"
)

type Config struct {
	// Include specifies a filter on the metrics that should be converted.
	Include MatchMetrics `mapstructure:"include"`
}

type MatchMetrics struct {
	filterset.Config `mapstructure:",squash"`

	Metrics []string `mapstructure:"metrics"`
}

// Verify Config implements Processor interface.
var _ component.Config = (*Config)(nil)

// Validate does not check for unsupported dimension key-value pairs, because those
// get silently dropped and ignored during translation.
func (config *Config) Validate() error {
	if len(config.Include.Metrics) > 0 && len(config.Include.MatchType) == 0 {
		return fmt.Errorf("match_type must be set if metrics are supplied")
	}
	if len(config.Include.MatchType) > 0 && len(config.Include.Metrics) == 0 {
		return fmt.Errorf("metrics must be supplied if match_type is set")
	}
	return nil
}
