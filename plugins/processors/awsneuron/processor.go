// Copyright Amazon.com, Inc. or its affiliates. All Rights Reserved.
// SPDX-License-Identifier: MIT

package awsneuron

import (
	"context"
	"fmt"
	"github.com/aws/amazon-cloudwatch-agent/plugins/processors/awsneuron/internal"
	"go.opentelemetry.io/collector/component"
	"go.opentelemetry.io/collector/pdata/pmetric"
	"go.uber.org/zap"
	"strings"
)

const (
	awsNeuronMetric = "neuron_"
)

type awsneuronprocessor struct {
	*Config
	logger         *zap.Logger
	cancelFunc     context.CancelFunc
	shutdownC      chan bool
	started        bool
	metricModifier *internal.MetricModifier
}

func newAwsNeuronProcessor(config *Config, logger *zap.Logger) *awsneuronprocessor {
	_, cancel := context.WithCancel(context.Background())
	d := &awsneuronprocessor{
		Config:         config,
		logger:         logger,
		cancelFunc:     cancel,
		metricModifier: internal.NewMetricModifier(logger),
	}
	return d
}

func (d *awsneuronprocessor) processMetrics(ctx context.Context, md pmetric.Metrics) (pmetric.Metrics, error) {
	if !d.started {
		return pmetric.NewMetrics(), nil
	}
	originalMd := pmetric.NewMetrics()
	md.CopyTo(originalMd)
	rms := md.ResourceMetrics()
	isNeuron := false
	for i := 0; i < rms.Len(); i++ {
		rs := rms.At(i)
		ilms := rs.ScopeMetrics()
		for j := 0; j < ilms.Len(); j++ {
			ils := ilms.At(j)
			metrics := ils.Metrics()

			newMetrics := pmetric.NewMetricSlice()
			for k := 0; k < metrics.Len(); k++ {
				m := metrics.At(k)
				if strings.Contains(m.Name(), "neuron") && !isNeuron {
					isNeuron = true
					d.logMd(md, "neuron metrics object before modification")
				}
				d.metricModifier.ModifyMetric(m).MoveAndAppendTo(newMetrics)
			}
			newMetrics.CopyTo(metrics)
		}
	}
	d.logMd(md, "neuron metrics object after modification")
	return md, nil
}

func (d *awsneuronprocessor) Shutdown(context.Context) error {
	close(d.shutdownC)
	d.cancelFunc()
	return nil
}

func (d *awsneuronprocessor) Start(ctx context.Context, _ component.Host) error {
	d.shutdownC = make(chan bool)
	d.started = true
	return nil
}

func (dc *awsneuronprocessor) logMd(md pmetric.Metrics, mdName string) {
	var logMessage strings.Builder

	logMessage.WriteString(fmt.Sprintf("\"%s\" : {\n", mdName))
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

	dc.logger.Info(logMessage.String())
}
