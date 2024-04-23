// Copyright The OpenTelemetry Authors
// SPDX-License-Identifier: Apache-2.0

package deltatosparseprocessor // import "github.com/open-telemetry/opentelemetry-collector-contrib/processor/deltatosparseprocessor"

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/collector/component"
	"go.opentelemetry.io/collector/component/componenttest"
	"go.opentelemetry.io/collector/consumer/consumertest"
	"go.opentelemetry.io/collector/processor/processortest"
)

func TestCreateDefaultConfig(t *testing.T) {
	factory := NewFactory()
	require.NotNil(t, factory)

	cfg := factory.CreateDefaultConfig()
	assert.NotNil(t, cfg, "failed to create default config")
	assert.NoError(t, componenttest.CheckConfigStruct(cfg))
}

func TestCreateProcessor(t *testing.T) {
	factory := NewFactory()
	require.NotNil(t, factory)

	cfg := factory.CreateDefaultConfig()
	setting := processortest.NewNopCreateSettings()

	tProcessor, err := factory.CreateTracesProcessor(context.Background(), setting, cfg, consumertest.NewNop())
	assert.Equal(t, err, component.ErrDataTypeIsNotSupported)
	assert.Nil(t, tProcessor)

	mProcessor, err := factory.CreateMetricsProcessor(context.Background(), setting, cfg, consumertest.NewNop())
	assert.NoError(t, err)
	assert.NotNil(t, mProcessor)

	lProcessor, err := factory.CreateLogsProcessor(context.Background(), setting, cfg, consumertest.NewNop())
	assert.Equal(t, err, component.ErrDataTypeIsNotSupported)
	assert.Nil(t, lProcessor)
}
