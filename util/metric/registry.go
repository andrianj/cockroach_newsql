// Copyright 2015 The Cockroach Authors.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or
// implied. See the License for the specific language governing
// permissions and limitations under the License.
//
// Author: Tobias Schottdorf (tobias.schottdorf@gmail.com)

package metric

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"regexp"

	"github.com/cockroachdb/cockroach/util/syncutil"
	"github.com/gogo/protobuf/proto"
	prometheusgo "github.com/prometheus/client_model/go"
	"github.com/prometheus/common/expfmt"
)

const sep = "-"

// DefaultTimeScales are the durations used for helpers which create windowed
// metrics in bulk (such as Latency or Rates).
var DefaultTimeScales = []TimeScale{Scale1M, Scale10M, Scale1H}

// A Registry bundles up various iterables (i.e. typically metrics or other
// registries) to provide a single point of access to them.
//
// A Registry can be added to another Registry through the Add/MustAdd methods. This allows a
// hierarchy of Registry instances to be created.
type Registry struct {
	syncutil.Mutex
	tracked map[string]Iterable
}

// NewRegistry creates a new Registry.
func NewRegistry() *Registry {
	return &Registry{
		tracked: map[string]Iterable{},
	}
}

// AddMetric adds the passed-in metric to the registry.
func (r *Registry) AddMetric(metric Iterable) {
	r.Lock()
	defer r.Unlock()
	r.tracked[metric.GetName()] = metric
}

// AddMetricGroup expands the metric group and adds all of them
// as individual metrics to the registry.
func (r *Registry) AddMetricGroup(group metricGroup) {
	r.Lock()
	defer r.Unlock()
	group.iterate(func(metric Iterable) {
		r.tracked[metric.GetName()] = metric
	})
}

// Add links the given Iterable into this registry using the given format
// string. The individual items in the registry will be formatted via
// fmt.Sprintf(format, <name>). As a special case, *Registry implements
// Iterable and can thus be added.
// Metric types in this package have helpers that allow them to be created
// and registered in a single step. Add is called manually only when adding
// a registry to another, or when integrating metrics defined elsewhere.
func (r *Registry) Add(format string, item Iterable) error {
	r.Lock()
	defer r.Unlock()
	if _, ok := r.tracked[format]; ok {
		return errors.New("format string already in use")
	}
	r.tracked[format] = item
	return nil
}

// MustAdd calls Add and panics on error.
func (r *Registry) MustAdd(format string, item Iterable) {
	if err := r.Add(format, item); err != nil {
		panic(fmt.Sprintf("error adding %s: %s", format, err))
	}
}

// Each calls the given closure for all metrics.
func (r *Registry) Each(f func(name string, val interface{})) {
	r.Lock()
	defer r.Unlock()
	for format, registry := range r.tracked {
		registry.Each(func(name string, v interface{}) {
			if name == "" {
				f(format, v)
			} else {
				f(fmt.Sprintf(format, name), v)
			}
		})
	}
}

// MarshalJSON marshals to JSON.
func (r *Registry) MarshalJSON() ([]byte, error) {
	m := make(map[string]interface{})
	r.Each(func(name string, v interface{}) {
		m[name] = v
	})
	return json.Marshal(m)
}

var (
	nameReplaceRE = regexp.MustCompile("[.-]")
)

// exportedName takes a metric name and generates a valid prometheus name.
// see nameReplaceRE for characters to be replaces with '_'.
func exportedName(name string) string {
	return nameReplaceRE.ReplaceAllString(name, "_")
}

// PrintAsText outputs all metrics in text format.
func (r *Registry) PrintAsText(w io.Writer) error {
	var metricFamily prometheusgo.MetricFamily
	var ret error
	r.Each(func(name string, v interface{}) {
		if ret != nil {
			return
		}
		if metric, ok := v.(PrometheusExportable); ok {
			metricFamily.Reset()
			metricFamily.Name = proto.String(exportedName(name))
			metric.FillPrometheusMetric(&metricFamily)
			if _, err := expfmt.MetricFamilyToText(w, &metricFamily); err != nil {
				ret = err
			}
		}
	})
	return ret
}

// GetCounter returns the Counter in this registry with the given name. If a
// Counter with this name is not present (including if a non-Counter Iterable is
// registered with the name), nil is returned.
func (r *Registry) GetCounter(name string) *Counter {
	r.Lock()
	defer r.Unlock()
	iterable, ok := r.tracked[name]
	if !ok {
		return nil
	}
	counter, ok := iterable.(*Counter)
	if !ok {
		return nil
	}
	return counter
}

// GetGauge returns the Gauge in this registry with the given name. If a Gauge
// with this name is not present (including if a non-Gauge Iterable is
// registered with the name), nil is returned.
func (r *Registry) GetGauge(name string) *Gauge {
	r.Lock()
	defer r.Unlock()
	iterable, ok := r.tracked[name]
	if !ok {
		return nil
	}
	gauge, ok := iterable.(*Gauge)
	if !ok {
		return nil
	}
	return gauge
}

// GetRate returns the Rate in this registry with the given name. If a Rate with
// this name is not present (including if a non-Rate Iterable is registered with
// the name), nil is returned.
func (r *Registry) GetRate(name string) *Rate {
	r.Lock()
	defer r.Unlock()
	iterable, ok := r.tracked[name]
	if !ok {
		return nil
	}
	rate, ok := iterable.(*Rate)
	if !ok {
		return nil
	}
	return rate
}
