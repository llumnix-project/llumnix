package structs

import (
	"fmt"
	"strings"
)

type Label struct {
	Name  string
	Value string
}

type Labels []Label

func NewLabels(labels []Label) Labels {
	return labels
}

func (ls Labels) Append(l Label) Labels {
	newLs := make([]Label, len(ls))
	copy(newLs, ls)
	newLs = append(newLs, l)
	return newLs
}

func (ls Labels) Flatten() string {
	var vec []string
	for _, l := range ls {
		vec = append(vec, fmt.Sprintf("%s=%s", l.Name, l.Value))
	}
	return strings.Join(vec, ";")
}

func (ls Labels) FlattenWithKey(k string) string {
	if len(k) <= 0 {
		panic("exception: empty key")
	}
	return fmt.Sprintf("%s;%s", k, ls.Flatten())
}

func (ls Labels) Convert() map[string]string {
	tags := make(map[string]string)
	for _, l := range ls {
		tags[l.Name] = l.Value
	}
	return tags
}
