// Copyright Amazon.com, Inc. or its affiliates. All Rights Reserved.
// SPDX-License-Identifier: MIT

package cumulativetodeltaprocessor

import (
	"github.com/aws/amazon-cloudwatch-agent/translator/translate/otel/receiver/awscontainerinsight"
	"github.com/open-telemetry/opentelemetry-collector-contrib/processor/cumulativetodeltaprocessor"
	"go.opentelemetry.io/collector/component"
	"go.opentelemetry.io/collector/confmap"
	"go.opentelemetry.io/collector/processor"

	"github.com/aws/amazon-cloudwatch-agent/translator/translate/otel/common"
)

const (
	// Match types are in internal package from contrib
	// Strict is the FilterType for filtering by exact string matches.
	strict = "strict"
	regexp = "regexp"
)

var (
	netKey    = common.ConfigKey(common.MetricsKey, common.MetricsCollectedKey, common.NetKey)
	diskioKey = common.ConfigKey(common.MetricsKey, common.MetricsCollectedKey, common.DiskIOKey)
)

type translator struct {
	name    string
	factory processor.Factory
}

var _ common.Translator[component.Config] = (*translator)(nil)

func NewTranslator() common.Translator[component.Config] {
	return NewTranslatorWithName("")
}

func NewTranslatorWithName(name string) common.Translator[component.Config] {
	return &translator{name, cumulativetodeltaprocessor.NewFactory()}
}

func (t *translator) ID() component.ID {
	return component.NewIDWithName(t.factory.Type(), t.name)
}

// Translate creates a processor config based on the fields in the
// Metrics section of the JSON config.
func (t *translator) Translate(conf *confmap.Conf) (component.Config, error) {
	//if conf == nil || (!conf.IsSet(diskioKey) && !conf.IsSet(netKey)) {
	//	return nil, &common.MissingKeyError{ID: t.ID(), JsonKey: fmt.Sprint(diskioKey, " or ", netKey)}
	//}
	cfg := t.factory.CreateDefaultConfig().(*cumulativetodeltaprocessor.Config)
	if awscontainerinsight.AcceleratedComputeMetricsEnabled(conf) {
		includeMetrics := []string{
			"node_neuron_execution_errors_generic",
			"node_neuron_execution_errors_numerical",
			"node_neuron_execution_errors_transient",
			"node_neuron_execution_errors_model",
			"node_neuron_execution_errors_runtime",
			"node_neuron_execution_errors_hardware",
			"node_neuron_execution_status_completed",
			"node_neuron_execution_status_timed_out",
			"node_neuron_execution_status_completed_with_err",
			"node_neuron_execution_status_completed_with_num_err",
			"node_neuron_execution_status_incorrect_input",
			"node_neuron_execution_status_failed_to_queue",
			"node_neuron_execution_errors_total",
			"container_neurondevice_hw_ecc_events_mem_ecc_corrected",
			"container_neurondevice_hw_ecc_events_mem_ecc_uncorrected",
			"container_neurondevice_hw_ecc_events_sram_ecc_corrected",
			"container_neurondevice_hw_ecc_events_sram_ecc_uncorrected",
			"container_neurondevice_hw_ecc_events_total",
			"pod_neurondevice_hw_ecc_events_mem_ecc_corrected",
			"pod_neurondevice_hw_ecc_events_mem_ecc_uncorrected",
			"pod_neurondevice_hw_ecc_events_sram_ecc_corrected",
			"pod_neurondevice_hw_ecc_events_sram_ecc_uncorrected",
			"pod_neurondevice_hw_ecc_events_total",
			"node_neurondevice_hw_ecc_events_mem_ecc_corrected",
			"node_neurondevice_hw_ecc_events_mem_ecc_uncorrected",
			"node_neurondevice_hw_ecc_events_sram_ecc_corrected",
			"node_neurondevice_hw_ecc_events_sram_ecc_uncorrected",
			"node_neurondevice_hw_ecc_events_total",
		}
		cfg.Include.Metrics = includeMetrics
		cfg.Include.MatchType = strict
	}

	//excludeMetrics := t.getExcludeNetAndDiskIOMetrics(conf)

	//if len(excludeMetrics) != 0 {
	//	cfg.Exclude.MatchType = strict
	//	cfg.Exclude.Metrics = excludeMetrics
	//}
	return cfg, nil
}

// DiskIO and Net Metrics are cumulative metrics
// DiskIO: https://github.com/shirou/gopsutil/blob/master/disk/disk.go#L32-L47
// Net: https://github.com/shirou/gopsutil/blob/master/net/net.go#L13-L25
// However, CloudWatch  does have an upper bound https://docs.aws.amazon.com/AmazonCloudWatch/latest/APIReference/API_PutMetricData.html
// Therefore, we calculate the delta values for customers instead of using the original values
// https://github.com/aws/amazon-cloudwatch-agent/blob/5ace5aa6d817684cf82f4e6aa82d9596fb56d74b/translator/translate/metrics/util/deltasutil.go#L33-L65
func (t *translator) getExcludeNetAndDiskIOMetrics(conf *confmap.Conf) []string {
	var excludeMetricName []string
	if conf.IsSet(diskioKey) {
		excludeMetricName = append(excludeMetricName, "iops_in_progress", "diskio_iops_in_progress")
	}
	return excludeMetricName
}
