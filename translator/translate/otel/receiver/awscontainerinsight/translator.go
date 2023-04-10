// Copyright Amazon.com, Inc. or its affiliates. All Rights Reserved.
// SPDX-License-Identifier: MIT

package awscontainerinsight

import (
	"strings"
	"time"

	"github.com/open-telemetry/opentelemetry-collector-contrib/receiver/awscontainerinsightreceiver"
	"go.opentelemetry.io/collector/component"
	"go.opentelemetry.io/collector/confmap"
	"go.opentelemetry.io/collector/receiver"

	"github.com/aws/private-amazon-cloudwatch-agent-staging/internal/util/collections"
	"github.com/aws/private-amazon-cloudwatch-agent-staging/translator/translate/otel/common"
)

// container orchestrator keys
const (
	ecs = "ecs"
	eks = "eks"

	defaultMetricsCollectionInterval = time.Minute
)

type translator struct {
	name    string
	factory receiver.Factory
	// services is a slice of config keys to orchestrators.
	services []*collections.Pair[string, string]
}

var _ common.Translator[component.Config] = (*translator)(nil)

// NewTranslator creates a new aws container insight receiver translator.
func NewTranslator() common.Translator[component.Config] {
	return NewTranslatorWithName("")
}

func NewTranslatorWithName(name string) common.Translator[component.Config] {
	baseKey := common.ConfigKey(common.LogsKey, common.MetricsCollectedKey)
	return &translator{
		name:    name,
		factory: awscontainerinsightreceiver.NewFactory(),
		services: []*collections.Pair[string, string]{
			{Key: common.ConfigKey(baseKey, common.ECSKey), Value: ecs},
			{Key: common.ConfigKey(baseKey, common.KubernetesKey), Value: eks},
		},
	}
}

func (t *translator) ID() component.ID {
	return component.NewIDWithName(t.factory.Type(), t.name)
}

// Translate creates an aws container insights receiver config if either
// of the sections defined in the services exist.
func (t *translator) Translate(conf *confmap.Conf) (component.Config, error) {
	configuredService := t.getConfiguredContainerService(conf)
	if configuredService == nil {
		var keys []string
		for _, service := range t.services {
			keys = append(keys, service.Key)
		}
		return nil, &common.MissingKeyError{ID: t.ID(), JsonKey: strings.Join(keys, " or ")}
	}
	cfg := t.factory.CreateDefaultConfig().(*awscontainerinsightreceiver.Config)
	intervalKeyChain := []string{
		common.ConfigKey(configuredService.Key, common.MetricsCollectionIntervalKey),
		common.ConfigKey(common.AgentKey, common.MetricsCollectionIntervalKey),
	}
	cfg.CollectionInterval = common.GetOrDefaultDuration(conf, intervalKeyChain, defaultMetricsCollectionInterval)
	cfg.ContainerOrchestrator = configuredService.Value
	return cfg, nil
}

// getConfiguredContainerService gets the first found container service
// from the service slice.
func (t *translator) getConfiguredContainerService(conf *confmap.Conf) *collections.Pair[string, string] {
	var configuredService *collections.Pair[string, string]
	if conf != nil {
		for _, service := range t.services {
			if conf.IsSet(service.Key) {
				configuredService = service
				break
			}
		}
	}
	return configuredService
}