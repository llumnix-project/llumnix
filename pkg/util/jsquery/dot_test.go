package jsquery

import (
	"fmt"
	"reflect"
	"strings"
	"testing"
)

func TestGetDottedStyleItem(t *testing.T) {
	testCases := []struct {
		have string
		key  string
		want map[string]interface{}
	}{
		{
			have: `{
					   "eas": {
						   "handlers": {
							  "disable_failure_handler": true
						   }
		              }
		            }`,
			want: map[string]interface{}{"handlers.disable_failure_handler": "true"},
			key:  "eas",
		},
		{
			have: `{
						"features": {
							"sigma": {
							  "alibaba-inc": {
								"com/app-stage": "PRE_PUBLISH",
								"com/app-stage1": "PRE_PUBLISH2",
								"com/xxx-version": 100
							  }
		             }
					  }
					}`,
			want: map[string]interface{}{
				"sigma.alibaba-inc.com/app-stage":   "PRE_PUBLISH",
				"sigma.alibaba-inc.com/app-stage1":  "PRE_PUBLISH2",
				"sigma.alibaba-inc.com/xxx-version": "100",
			},
			key: "features",
		},
		{
			have: `{
			    "labels": {
					"eas.aliyun.com/test": "aaa",
					"aaa.bbb": "ccc",
                    "qqq": "ppp"
			   }
			}`,
			want: map[string]interface{}{
				"eas.aliyun.com/test": "aaa",
				"aaa.bbb":             "ccc",
				"qqq":                 "ppp",
			},
			key: "labels",
		},
	}

	for _, tc := range testCases {
		jq, err := NewStringQuery(tc.have)
		if err != nil {
			t.Fatal(err)
		}
		have, err := GetDottedStyleItem(jq, tc.key)
		if err != nil {
			t.Fatal(err)
		}
		if reflect.DeepEqual(tc.want, have) {
			t.Logf("have:%v, want:%v", have, tc.want)
		} else {
			t.Errorf("have:%v, want:%v", have, tc.want)
		}
	}
}

func TestJQ(t *testing.T) {
	js := `{
		"SDProxy": {
           "webui_service_name": "foo",
           "token": "nxedf",
           "service_cmd": "./webui.sh --listen --port=8000" 
		}
	}`
	want := map[string]interface{}{
		"webui_service_name": "foo",
		"token":              "nxedf",
		"service_cmd":        "./webui.sh --listen --port=8000",
	}
	jq, _ := NewStringQuery(js)
	//proxyJq, _ := jq.QueryToJq("SDProxy")
	proxyCfg, _ := jq.QueryToMap("SDProxy")
	for k, v := range proxyCfg {
		fmt.Println(strings.ToUpper(k), v)
	}
	if !reflect.DeepEqual(want, proxyCfg) {
		t.Errorf("have:%v, want:%v", proxyCfg, want)
	}
}

func TestGetDottedStyleItem_Features(t *testing.T) {
	js := `{
		"SDProxy": {
           "webui_service_name": "foo",
           "token": "nxedf",
           "service_cmd": "./webui.sh --listen --port=8000" 
		},
		"features": {
        	"eas.aliyun.com/restart_pod_with_gpu_errors": {
				"metrics": 
					[
						"node_gpu_ecc_total_vol_sbe",
            			"node_gpu_ecc_total_vol_dbe",
            			"node_gpu_kernel_err"
        			]
			}
    	}
	}`
	want := map[string]interface{}{
		"eas.aliyun.com/restart_pod_with_gpu_errors.metrics[0]": "node_gpu_ecc_total_vol_sbe",
		"eas.aliyun.com/restart_pod_with_gpu_errors.metrics[1]": "node_gpu_ecc_total_vol_dbe",
		"eas.aliyun.com/restart_pod_with_gpu_errors.metrics[2]": "node_gpu_kernel_err",
	}
	jq, _ := NewStringQuery(js)

	have, err := GetDottedStyleItem(jq, "features")
	if err != nil {
		t.Fatal(err)
	}
	fmt.Printf("%+v\n", have)
	if !reflect.DeepEqual(want, have) {
		t.Errorf("have:%v, want:%v", have, want)
	}
}
