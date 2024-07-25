// Copyright Amazon.com, Inc. or its affiliates. All Rights Reserved.
// SPDX-License-Identifier: MIT

package gpuattributes

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"go.opentelemetry.io/collector/pdata/pcommon"
	"go.opentelemetry.io/collector/pdata/pmetric"
	"go.uber.org/zap"

	"github.com/aws/amazon-cloudwatch-agent/internal/containerinsightscommon"
	"github.com/aws/amazon-cloudwatch-agent/plugins/processors/gpuattributes/internal"
	"github.com/aws/amazon-cloudwatch-agent/plugins/processors/gpuattributes/internal/metricFilters"
)

const (
	gpuMetricIdentifier   = "_gpu_"
	containerMetricPrefix = "container_"
	podMetricPrefix       = "pod_"
	nodeMetricPrefix      = "node_"
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

			d.filterGpuMetricsWithoutPodName(metrics, rs.Resource().Attributes())

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
				d.processMetricAttributes(m)
			}
		}

		dropResourceMetricAttributes(rs)

		if isNeuronMetrics {
			d.logMd(originalMd, "GPU_Processor_Neuron_Before")
			d.logMd(md, "GPU_Processor_Neuron_After")
		}
	}
	return md, nil
}

func (d *gpuAttributesProcessor) processMetricAttributes(m pmetric.Metric) {
	// only decorate GPU metrics
	isGpuMetric := strings.Contains(m.Name(), gpuMetricIdentifier)
	isNeuronMetric := d.awsNeuronMetricModifier.IsProcessedNeuronMetric(m.Name())
	if !isNeuronMetric && !isGpuMetric {
		return
	}

	labelFilter := map[string]map[string]interface{}{}
	if isGpuMetric {
		if strings.HasPrefix(m.Name(), containerMetricPrefix) {
			labelFilter = metricFilters.ContainerLabelFilter
		} else if strings.HasPrefix(m.Name(), podMetricPrefix) {
			labelFilter = metricFilters.PodLabelFilter
		} else if strings.HasPrefix(m.Name(), nodeMetricPrefix) {
			labelFilter = metricFilters.NodeLabelFilter
		}
	} else if isNeuronMetric {
		if strings.HasPrefix(m.Name(), containerMetricPrefix) {
			labelFilter = metricFilters.ContainerNeuronMetricFilter
		} else if strings.HasPrefix(m.Name(), podMetricPrefix) {
			labelFilter = metricFilters.PodNeuronMetricFilter
		} else if strings.HasPrefix(m.Name(), nodeMetricPrefix) {
			labelFilter = metricFilters.NodeNeuronMetricFilter
		}

		if strings.Contains(m.Name(), "_neurondevice_hw") {
			delete(labelFilter[internal.Kubernetes], "labels")
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

// remove dcgm metrics that do not contain PodName attribute which means there is no workload associated to container/pod
func (d *gpuAttributesProcessor) filterGpuMetricsWithoutPodName(metrics pmetric.MetricSlice, resourceAttributes pcommon.Map) {
	metrics.RemoveIf(func(m pmetric.Metric) bool {
		isGpu := strings.Contains(m.Name(), gpuMetricIdentifier)
		isContainerOrPod := strings.HasPrefix(m.Name(), containerMetricPrefix) || strings.HasPrefix(m.Name(), podMetricPrefix)
		if !isGpu || !isContainerOrPod {
			return false
		}

		_, hasPodAtResource := resourceAttributes.Get(internal.PodName)
		var dps pmetric.NumberDataPointSlice
		switch m.Type() {
		case pmetric.MetricTypeGauge:
			dps = m.Gauge().DataPoints()
		case pmetric.MetricTypeSum:
			dps = m.Sum().DataPoints()
		default:
			d.logger.Debug("Ignore unknown metric type", zap.String(containerinsightscommon.MetricType, m.Type().String()))
		}

		dps.RemoveIf(func(dp pmetric.NumberDataPoint) bool {
			_, hasPodInfo := dp.Attributes().Get(internal.PodName)
			return !hasPodInfo && !hasPodAtResource
		})
		return dps.Len() == 0
	})
}

func dropResourceMetricAttributes(resourceMetric pmetric.ResourceMetrics) {
	serviceNameKey := "service.name"
	attributes := resourceMetric.Resource().Attributes()
	serviceName, exists := attributes.Get(serviceNameKey)

	if exists && (serviceName.Str() == "containerInsightsNeuronMonitorScraper" || serviceName.Str() == "containerInsightsDCGMExporterScraper") {
		resourceMetric.Resource().Attributes().Clear()
	}
}

func (d *gpuAttributesProcessor) logMd(md pmetric.Metrics, name string) {
	var logMessage strings.Builder

	logMessage.WriteString(fmt.Sprintf("\"%s_METRICS_MD\" : {\n", name))
	rms := md.ResourceMetrics()
	for i := 0; i < rms.Len(); i++ {
		rs := rms.At(i)
		rs.Resource().Attributes().AsRaw()
		ilms := rs.ScopeMetrics()
		logMessage.WriteString(fmt.Sprintf("\t\"ResourceMetric_%d\": {\n", i))
		logMessage.WriteString(fmt.Sprintf("\t\t\"Resource attributes\": %s,\n", rs.Resource().Attributes().AsRaw()))
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
