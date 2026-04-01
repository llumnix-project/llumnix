package metrics

import (
	"strings"

	"github.com/prometheus/client_golang/prometheus"
)

type Label struct {
	Name  string
	Value string
}

type Labels []Label

func (ls Labels) Append(l Label) Labels {
	newLs := make([]Label, len(ls))
	copy(newLs, ls)
	newLs = append(newLs, l)
	return newLs
}

func (ls Labels) Flatten() string {
	var vec []string
	for _, l := range ls {
		vec = append(vec, l.Name+"="+l.Value)
	}
	return strings.Join(vec, ";")
}

// Names returns the label names as a string slice.
// Used to declare label dimensions when creating prometheus *Vec types.
func (ls Labels) Names() []string {
	names := make([]string, len(ls))
	for i, l := range ls {
		names[i] = l.Name
	}
	return names
}

// ToPrometheusLabels converts Labels to prometheus.Labels map.
// Used to resolve a specific metric instance from a *Vec type.
func (ls Labels) ToPrometheusLabels() prometheus.Labels {
	m := make(prometheus.Labels, len(ls))
	for _, l := range ls {
		m[l.Name] = l.Value
	}
	return m
}
