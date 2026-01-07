package jsquery

import (
	"bytes"
	"strings"

	"easgo/pkg/util/gron"
)

// GetDottedStyleItem gets a dotted-styled item from a json object.
//
// A dotted-styled item example: "eas.handlers.disable_failure_handler": true
//
// we got a json like:
//{
//  "eas": {
//    "handlers": {
//      "disable_failure_handler": true
//    }
//  }
//}
//
// we call GetDottedStyleItem(jq, "eas"), the result like: map[handlers.disable_failure_handler:true]

func GetDottedStyleItem(jq *JQ, key string) (map[string]interface{}, error) {
	if _, err := jq.QueryToMap(key); err != nil {
		return nil, err
	}
	fe, err := jq.Query(key)
	if err != nil {
		return nil, err
	}
	br := bytes.NewBuffer(NewQuery(fe).Bytes())
	stats, err := gron.Gron(br, gron.OptMonochrome)
	if err != nil {
		return nil, err
	}

	mp := map[string]interface{}{}
	for _, stat := range gron.Simplify(stats) {
		if strings.HasPrefix(stat, "[\"") {
			stat = strings.Replace(stat, "[\"", "", 1)
		}
		stat = strings.Replace(stat, "[\"", ".", -1)
		stat = strings.Replace(stat, "\"]", "", -1)
		fields := strings.Split(stat, " = ")
		if len(fields) == 2 {
			mp[fields[0]] = strings.Replace(fields[1], "\"", "", -1)
		}
	}
	return mp, nil
}
