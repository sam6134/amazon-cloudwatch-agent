// Copyright Amazon.com, Inc. or its affiliates. All Rights Reserved.
// SPDX-License-Identifier: MIT

package gpuattributes

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/aws/amazon-cloudwatch-agent/plugins/processors/gpuattributes/internal"
	"github.com/aws/amazon-cloudwatch-agent/plugins/processors/gpuattributes/internal/metricFilters"
	"strings"

	"go.opentelemetry.io/collector/pdata/pcommon"
	"go.opentelemetry.io/collector/pdata/pmetric"
	"go.uber.org/zap"

	"github.com/aws/amazon-cloudwatch-agent/internal/containerinsightscommon"
)

const (
	gpuMetricIdentifier      = "_gpu_"
	gpuContainerMetricPrefix = "container_"
	gpuPodMetricPrefix       = "pod_"
	gpuNodeMetricPrefix      = "node_"
)

// schemas at each resource level
// - Container Schema
//   - ClusterName
//   - ClusterName, Namespace, PodName, ContainerName
//   - ClusterName, Namespace, PodName, FullPodName, ContainerName
//   - ClusterName, Namespace, PodName, FullPodName, ContainerName, GpuDevice
//
// - Pod
//   - ClusterName
//   - ClusterName, Namespace
//   - ClusterName, Namespace, Service
//   - ClusterName, Namespace, PodName
//   - ClusterName, Namespace, PodName, FullPodName
//   - ClusterName, Namespace, PodName, FullPodName, GpuDevice
//
// - Node
//   - ClusterName
//   - ClusterName, InstanceIdKey, NodeName
//   - ClusterName, InstanceIdKey, NodeName, GpuDevice
type gpuAttributesProcessor struct {
	*Config
	logger                          *zap.Logger
	awsNeuronMetricModifier         *internal.AwsNeuronMetricModifier
	awsNeuronMemoryMetricAggregator *internal.AwsNeuronMemoryMetricsAggregator
}

func newGpuAttributesProcessor(config *Config, logger *zap.Logger) *gpuAttributesProcessor {
	d := &gpuAttributesProcessor{
		Config:                          config,
		logger:                          logger,
		awsNeuronMetricModifier:         internal.NewMetricModifier(logger),
		awsNeuronMemoryMetricAggregator: internal.NewMemoryMemoryAggregator(),
	}
	return d
}

func (d *gpuAttributesProcessor) processMetrics(_ context.Context, md pmetric.Metrics) (pmetric.Metrics, error) {
	isNeuronMetrics := false
	originalMd := pmetric.NewMetrics()
	md.CopyTo(originalMd)
	rms := md.ResourceMetrics()
	for i := 0; i < rms.Len(); i++ {
		rs := rms.At(i)
		ilms := rs.ScopeMetrics()
		for j := 0; j < ilms.Len(); j++ {
			ils := ilms.At(j)
			metrics := ils.Metrics()

			// loop over all the original metrics and add aggregated and granular metrics at end of the list
			metricsLength := metrics.Len()
			for k := 0; k < metricsLength; k++ {
				m := metrics.At(k)
				if strings.Contains(m.Name(), "neuron") || strings.Contains(m.Name(), "Neuron") {
					isNeuronMetrics = true
				}
				d.awsNeuronMemoryMetricAggregator.AggregateMemoryMetric(m)
				// non neuron metric is returned as a singleton list
				d.awsNeuronMetricModifier.ModifyMetric(m, metrics)
			}
			if d.awsNeuronMemoryMetricAggregator.MemoryMetricsFound {
				aggregatedMemoryMetric := d.awsNeuronMemoryMetricAggregator.FlushAggregatedMemoryMetric()
				d.awsNeuronMetricModifier.ModifyMetric(aggregatedMemoryMetric, metrics)
			}

			//loop over all metrics and filter labels
			for k := 0; k < metrics.Len(); k++ {
				m := metrics.At(k)
				d.processGPUMetricAttributes(m)
			}
		}
	}
	if isNeuronMetrics {
		//d.logMd(originalMd, "ORIGINAL_NEURON_METRICS")
		d.logMd(md, "MODIFIED_NEURON_METRICS")
	}
	return md, nil
}

func (d *gpuAttributesProcessor) processGPUMetricAttributes(m pmetric.Metric) {
	// only decorate GPU metrics
	isGpuMetric := strings.Contains(m.Name(), gpuMetricIdentifier)
	isNeuronMetric := d.awsNeuronMetricModifier.IsProcessedNeuronMetric(m.Name())
	if !isNeuronMetric || !isGpuMetric {
		return
	}

	labelFilter := map[string]map[string]interface{}{}
	if isGpuMetric {
		if strings.HasPrefix(m.Name(), gpuContainerMetricPrefix) {
			labelFilter = metricFilters.ContainerLabelFilter
		} else if strings.HasPrefix(m.Name(), gpuPodMetricPrefix) {
			labelFilter = metricFilters.PodLabelFilter
		} else if strings.HasPrefix(m.Name(), gpuNodeMetricPrefix) {
			labelFilter = metricFilters.NodeLabelFilter
		}
	} else if isNeuronMetric {
		labelFilter = metricFilters.CommonNeuronMetricFilter
		if strings.HasPrefix(m.Name(), "node_neurondevice_") {
			labelFilter = metricFilters.NodeAWSNeuronDeviceMetricFilter
		}
	}

	var dps pmetric.NumberDataPointSlice
	switch m.Type() {
	case pmetric.MetricTypeGauge:
		dps = m.Gauge().DataPoints()
	case pmetric.MetricTypeSum:
		dps = m.Sum().DataPoints()
	default:
		d.logger.Debug("Ignore unknown metric type", zap.String(containerinsightscommon.MetricType, m.Type().String()))
	}

	for i := 0; i < dps.Len(); i++ {
		d.filterAttributes(dps.At(i).Attributes(), labelFilter)
	}
}

func (d *gpuAttributesProcessor) filterAttributes(attributes pcommon.Map, labels map[string]map[string]interface{}) {
	if len(labels) == 0 {
		return
	}
	// remove labels that are not in the keep list
	attributes.RemoveIf(func(k string, _ pcommon.Value) bool {
		if _, ok := labels[k]; ok {
			return false
		}
		return true
	})

	// if a label has child level filter list, that means the label is map type
	// only handles map type since there are currently only map and value types with GPU
	for lk, ls := range labels {
		if len(ls) == 0 {
			continue
		}
		if av, ok := attributes.Get(lk); ok {
			// decode json formatted string value into a map then encode again after filtering elements
			var blob map[string]json.RawMessage
			strVal := av.Str()
			err := json.Unmarshal([]byte(strVal), &blob)
			if err != nil {
				d.logger.Warn("gpuAttributesProcessor: failed to unmarshal label", zap.String("label", lk))
				continue
			}
			newBlob := make(map[string]json.RawMessage)
			for bkey, bval := range blob {
				if _, ok := ls[bkey]; ok {
					newBlob[bkey] = bval
				}
			}
			bytes, err := json.Marshal(newBlob)
			if err != nil {
				d.logger.Warn("gpuAttributesProcessor: failed to marshall label", zap.String("label", lk))
				continue
			}
			attributes.PutStr(lk, string(bytes))
		}
	}
}

func (d *gpuAttributesProcessor) logMd(md pmetric.Metrics, name string) {
	var logMessage strings.Builder

	logMessage.WriteString(fmt.Sprintf("\"%s_METRICS_MD\" : {\n", name))
	rms := md.ResourceMetrics()
	for i := 0; i < rms.Len(); i++ {
		rs := rms.At(i)
		ilms := rs.ScopeMetrics()
		logMessage.WriteString(fmt.Sprintf("\t\"ResourceMetric_%d\": {\n", i))
		for j := 0; j < ilms.Len(); j++ {
			ils := ilms.At(j)
			metrics := ils.Metrics()
			logMessage.WriteString(fmt.Sprintf("\t\t\"ScopeMetric_%d\": {\n", j))
			logMessage.WriteString(fmt.Sprintf("\t\t\"Metrics_%d\": [\n", j))

			for k := 0; k < metrics.Len(); k++ {
				m := metrics.At(k)
				logMessage.WriteString(fmt.Sprintf("\t\t\t\"Metric_%d\": {\n", k))
				logMessage.WriteString(fmt.Sprintf("\t\t\t\t\"name\": \"%s\",\n", m.Name()))
				logMessage.WriteString(fmt.Sprintf("\t\t\t\t\"type\": \"%s\",\n", m.Type()))

				var datapoints pmetric.NumberDataPointSlice
				switch m.Type() {
				case pmetric.MetricTypeGauge:
					datapoints = m.Gauge().DataPoints()
				case pmetric.MetricTypeSum:
					datapoints = m.Sum().DataPoints()
				default:
					datapoints = pmetric.NewNumberDataPointSlice()
				}

				logMessage.WriteString("\t\t\t\t\"datapoints\": [\n")
				for yu := 0; yu < datapoints.Len(); yu++ {
					logMessage.WriteString("\t\t\t\t\t{\n")
					logMessage.WriteString(fmt.Sprintf("\t\t\t\t\t\t\"attributes\": \"%v\",\n", datapoints.At(yu).Attributes().AsRaw()))
					logMessage.WriteString(fmt.Sprintf("\t\t\t\t\t\t\"value\": %v,\n", datapoints.At(yu).DoubleValue()))
					logMessage.WriteString(fmt.Sprintf("\t\t\t\t\t\t\"timestamp\": %v,\n", datapoints.At(yu).Timestamp()))
					logMessage.WriteString(fmt.Sprintf("\t\t\t\t\t\t\"flags\": %v,\n", datapoints.At(yu).Flags()))
					logMessage.WriteString(fmt.Sprintf("\t\t\t\t\t\t\"value type\": %v,\n", datapoints.At(yu).ValueType()))
					logMessage.WriteString("\t\t\t\t\t},\n")
				}
				logMessage.WriteString("\t\t\t\t],\n")
				logMessage.WriteString("\t\t\t},\n")
			}
			logMessage.WriteString("\t\t],\n")
			logMessage.WriteString("\t\t},\n")
		}
		logMessage.WriteString("\t},\n")
	}
	logMessage.WriteString("},\n")

	d.logger.Info(logMessage.String())
}
