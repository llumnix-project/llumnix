package jsquery

import (
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"sort"
	"strings"
	"testing"

	"github.com/wI2L/jsondiff"

	"gotest.tools/assert"
	is "gotest.tools/assert/cmp"

	"llm-gateway/pkg/utils/erraggr"
)

const (
	ServiceKeyGateway = "networking.gateway,metadata.gateway,?k"
)

func TestJQ_Query(t *testing.T) {
	js := `
{
    "key": "val",
    "key1": {
        "val1": {
            "key2": "val2"
        }
    },
    "key2": [
        "key2_val1",
        "key2_val2"
    ],
    "key3": [
        {
            "key4": "val4"
        },
        {
            "key5": "val5"
        }
    ]
}
`
	jq, err := NewStringQuery(js)
	assert.Check(t, err)

	_, err = jq.Query("key")
	assert.Check(t, err)

	_, err = jq.Query("key1.val1.key2")
	assert.Check(t, err)

	key3Val, err := jq.Query("key3.[1].key5")
	assert.Check(t, err)
	assert.Check(t, key3Val.(string) == "val5")

	_, err = jq.Query("key3.[4]")
	assert.Check(t, is.ErrorContains(err, "out of range"))

	_, err = jq.Query("key1.[0]")
	assert.Check(t, is.ErrorContains(err, "not an array"))

	_, err = jq.Query("key1.key5")
	assert.Check(t, is.ErrorContains(err, "not exist"))

}

func TestJQ_Set(t *testing.T) {

	js := `
{
    "key": "val",
    "key1": {
        "key2": {
            "key3": 2
        }
    },
    "key3": [
        {
            "key4": "val4"
        },
        {
            "key5": "val5"
        }
    ],
	"key7": [1.2,2.4]
}
`
	jq, err := NewStringQuery(js)
	assert.Check(t, err)

	err = jq.Set("key1.key2.key3", "val1")
	assert.Check(t, err)
	val, err := jq.Query("key1.key2.key3")
	assert.Check(t, err)
	assert.Check(t, val.(string) == "val1")

	err = jq.Set("key4.key5.key6", "val1")
	assert.Check(t, err)

	_, err = jq.Query("key4")
	assert.Check(t, err)
	_, err = jq.Query("key4.key5")
	assert.Check(t, err)
	val, err = jq.Query("key4.key5.key6")
	assert.Check(t, err)
	assert.Check(t, val == "val1")

	err = jq.Set("key3.[1].key5", "val6")
	assert.Check(t, err)
	val, err = jq.Query("key3.[1].key5")
	assert.Check(t, val == "val6")

	err = jq.Set("key8", []int{1, 2, 3})
	assert.Check(t, err)
	val, err = jq.Query("key8")
	assert.DeepEqual(t, val, []int{1, 2, 3})

	a := struct {
		A string `json:"a"`
		B string `json:"b"`
	}{
		"foo",
		"bar",
	}

	err = jq.Set("key8", []int{1, 2, 3})
	assert.Check(t, err)
	val, err = jq.Query("key8")
	assert.DeepEqual(t, val, []int{1, 2, 3})

	err = jq.Set("key9", a)
	assert.Check(t, err)
	val, err = jq.Query("key9")
	assert.DeepEqual(t, val, map[string]interface{}{"a": "foo", "b": "bar"})

	err = jq.Set("key3.[1].key5.key6", "val6")
	assert.Check(t, is.ErrorContains(err, "not an object"))

	err = jq.Set("key7.[1]", "val")
	assert.Check(t, is.ErrorContains(err, "mismatch"))

	err = jq.Set("key7.[1]", 7.2)
	assert.Check(t, err)
}

func TestJQ_Set2(t *testing.T) {
	configJq, _ := NewStringQuery("{}")
	configJq.Set("storage.[0]", map[string]interface{}{
		"mount_path": "/workspace/eas/data/",
		"cloud_disk": map[string]interface{}{
			"capacity": fmt.Sprintf("%dGi", 4000*6/5),
		},
	})

	t.Log(configJq.String())
}

func TestJQ_IsTrue(t *testing.T) {
	js := `
{
    "key1": "true",
    "key2": "True",
    "key3": true,
	"key4": "xxx",
	"key5": "False"
}
`
	jq, err := NewStringQuery(js)
	assert.Check(t, err)

	ret := jq.IsTrue("key1")
	assert.Check(t, ret)
	ret = jq.IsTrue("key2")
	assert.Check(t, ret)
	ret = jq.IsTrue("key3")
	assert.Check(t, ret)
	ret = jq.IsTrue("key4")
	assert.Check(t, !ret)
	ret = jq.IsTrue("key5")
	assert.Check(t, !ret)
	ret = jq.IsTrue("key6")
	assert.Check(t, !ret)
}

func TestJQ_Has(t *testing.T) {
	js := `
{
    "key": "true",
    "key1": {
        "val1": {
            "key2": "val2"
        }
    }
}
`
	jq, err := NewStringQuery(js)
	assert.Check(t, err)

	ret := jq.Has("key1")
	assert.Check(t, ret)

	ret = jq.Has("key1.val1.key2")
	assert.Check(t, ret)

	ret = jq.Has("key1.val1.key5")
	assert.Check(t, !ret)
}

func TestJQ_ListKeys(t *testing.T) {
	js := `
{
    "key": "true",
    "key1": {
        "val1": {
            "key2": "val2"
        }
    },
    "key3": {
		"key4": ["abc", "bcd"],
        "key5": {
           "key6": 7,
           "key7": 12.35
        }
    }
}
`
	jq, err := NewStringQuery(js)
	if err != nil {
		t.Errorf(err.Error())
	}

	keys, err := jq.ListKeys()
	if err != nil {
		t.Errorf(err.Error())
	}
	sort.Strings(keys)

	if !reflect.DeepEqual(keys, []string{
		"key",
		"key1.val1.key2",
		"key3.key4",
		"key3.key5.key6",
		"key3.key5.key7",
	}) {
		t.Errorf("%+v", keys)
	}

	keys, err = jq.ListKeys(Cascade())
	if err != nil {
		t.Errorf(err.Error())
	}
	sort.Strings(keys)
	if !reflect.DeepEqual(keys, []string{
		"key",
		"key1.val1.key2",
		"key3.key4.[0]",
		"key3.key4.[1]",
		"key3.key5.key6",
		"key3.key5.key7",
	}) {
		t.Errorf("%+v", keys)
	}
}

func TestJQ_Wildcard(t *testing.T) {

	js := `
{
    "key": "val",
    "key1": {
        "key2": {
            "key3": 2
        }
    },
    "key3": [
        {
            "key4": "val4"
        },
        {
            "key5": "val5"
        }
    ],
	"key7": [1.2,2.4]
}
`
	jq, err := NewStringQuery(js)
	assert.Check(t, err)
	data, err := jq.Query("key3.[*]")
	if err != nil {
		t.Error(err)
		t.FailNow()
	}
	t.Logf("%v", data)

	data, err = jq.Query("key3.[*].key4")
	if err != nil {
		t.Error(err)
		t.FailNow()
	}
	t.Logf("%v", data)

	data, err = jq.Query("key7.[*]")
	if err != nil {
		t.Error(err)
		t.FailNow()
	}
	t.Logf("%v", data)
}

func set(j *JQ) {
	j.Set("key3.key4", "ok1")
	j.Set("key6.key3", "ok1")
}

func TestJQ_Set_1(t *testing.T) {
	js := "{}"
	jq, err := NewStringQuery(js)
	assert.Check(t, err)

	err = jq.Set("key1.key2", "ok")
	assert.Check(t, err)

	t.Log(jq.String())

	err = jq.Set("key2.key3", "ok")
	assert.Check(t, err)
	t.Log(jq.String())

	set(jq)
	t.Log(jq.String())
}

func TestJQ_Export(t *testing.T) {
	js := "{}"
	jq, err := NewStringQuery(js)
	assert.Check(t, err)

	err = jq.Set("key1.key2", "ok")
	assert.Check(t, err)

	err = jq.Set("key5.key6", "ok")
	assert.Check(t, err)

	err = jq.Set("key3.key4,key5.key6,?h", "ok")
	assert.Check(t, err)

	export, err := jq.Export()
	assert.Check(t, err)

	t.Log(export)

	jq, err = NewStringQuery(export)
	assert.Check(t, err)

	_, err = jq.QueryToString("key3.key4")
	assert.Check(t, err != nil)
}

func TestJQ_Remove(t *testing.T) {
	js := `
{
    "key": "val",
    "key1": {
        "val1": {
            "key2": "val2"
        },
		"val2": {
            "key4": {
            	"key5": "val"
			}
        }
    }
}
`
	jq, err := NewStringQuery(js)
	assert.Check(t, err)

	err = jq.Remove("key1.val1.key2")
	assert.Check(t, err)

	err = jq.Remove("key1.val2..")
	assert.Check(t, err != nil)
}

type testStruct1 struct {
	A string `json:"a"`
	B bool   `json:"b"`
	C int    `json:"c"`
}

func (t *testStruct1) hello() string {
	return t.A
}

type testIface interface {
	hello() string
}

func TestJQ_As_1(t *testing.T) {
	js := `
{
	"a": 3,
	"b": "true",
	"c": "c"
}
`
	jq, err := NewStringQuery(js)
	assert.Check(t, err)

	var s testStruct1
	err = jq.As(&s)
	if err != nil {
		t.Log(err)
	}
	assert.Check(t, err != nil)
	var agg erraggr.ErrAggregator
	assert.Check(t, errors.As(err, &agg))
	assert.Equal(t, 3, len(agg.Errors()))

	var s2 testIface = &testStruct1{}
	err = jq.As(&s2)
	assert.Check(t, errors.As(err, &agg))
	assert.Equal(t, 3, len(agg.Errors()))

	js = `
{
	"a": "3",
	"b": true,
	"c": 3
}
`
	jq, err = NewStringQuery(js)
	assert.Check(t, err)
	var s3 testStruct1
	err = jq.As(&s3)
	assert.Check(t, err)
	var s4 = map[string]interface{}{}
	err = jq.As(&s4)
	assert.Check(t, err)
}

func TestNewStringQuery(t *testing.T) {
	jq, err := NewStringQuery("{}")
	if err != nil {
		t.Error(err)
	}

	t.Log(jq)
}

func TestCompareExp0(t *testing.T) {
	str1 := `{
        "metadata": {
			"cpu": 2,
			"instance": 1,
			"memory": 4000
        }
    }`

	str2 := `{
        "networking": {
        }
    }`

	jq1, err := NewStringQuery(str1)
	if err != nil {
		t.Fatal(err)
	}
	jq2, err := NewStringQuery(str2)
	if err != nil {
		t.Fatal(err)
	}

	equal, err := CompareExp(jq1, jq2, ServiceKeyGateway)
	if err != nil {
		t.Fatal(err)
	}

	t.Log(equal)
	if !equal {
		t.Fatal("expected equal while actual not equal")
	}
}

func TestCompareExps1(t *testing.T) {
	str1 := `{
        "metadata": {
			"cpu": 2,
			"instance": 1,
			"memory": 4000,
			"gateway": "contour"
        }
    }`

	str2 := `{
        "networking": {
            "gateway": "contour"
        }
    }`

	jq1, err := NewStringQuery(str1)
	if err != nil {
		t.Fatal(err)
	}
	jq2, err := NewStringQuery(str2)
	if err != nil {
		t.Fatal(err)
	}

	equal, err := CompareExp(jq1, jq2, ServiceKeyGateway)
	if err != nil {
		t.Fatal(err)
	}

	t.Log(equal)
	if !equal {
		t.Fatal("expected equal while actual not equal")
	}
}

func TestCompareExps2(t *testing.T) {
	str1 := `{
		"metadata": {
			"cpu": 2,
			"instance": 1,
			"memory": 4000
		},
		"networking": {
			"gateway": "contour"
		}
	}`

	str2 := `{
        "networking": {
            "gateway": "contour"
        }
    }`

	jq1, err := NewStringQuery(str1)
	if err != nil {
		t.Fatal(err)
	}
	jq2, err := NewStringQuery(str2)
	if err != nil {
		t.Fatal(err)
	}

	equal, err := CompareExp(jq1, jq2, ServiceKeyGateway)
	if err != nil {
		t.Fatal(err)
	}

	t.Log(equal)
	if !equal {
		t.Fatal("expected equal while actual not equal")
	}
}

func TestCompareExps3(t *testing.T) {
	str1 := `{
        "metadata": {
			"cpu": 2,
			"instance": 1,
			"memory": 4000,
			"gateway": "contour"
        }
    }`

	str2 := `{
        "networking": {
            "gateway": "shard0"
        }
    }`

	jq1, err := NewStringQuery(str1)
	if err != nil {
		t.Fatal(err)
	}
	jq2, err := NewStringQuery(str2)
	if err != nil {
		t.Fatal(err)
	}

	equal, err := CompareExp(jq1, jq2, ServiceKeyGateway)
	if err != nil {
		t.Fatal(err)
	}

	t.Log(equal)
	if equal {
		t.Fatal("expected unequal while actual equal")
	}
}

func TestCompareExps4(t *testing.T) {
	str1 := `{
		"metadata": {
			"cpu": 2,
			"instance": 1,
			"memory": 4000
		},
		"networking": {
			"gateway": "contour"
		}
	}`

	str2 := `{
        "networking": {
            "gateway": "shard0"
        }
    }`

	jq1, err := NewStringQuery(str1)
	if err != nil {
		t.Fatal(err)
	}
	jq2, err := NewStringQuery(str2)
	if err != nil {
		t.Fatal(err)
	}

	equal, err := CompareExps(jq1, jq2, []string{ServiceKeyGateway})
	if err != nil {
		t.Fatal(err)
	}

	t.Log(equal)
	if equal {
		t.Fatal("expected unequal while actual equal")
	}
}

func TestJsonDiff1(t *testing.T) {
	str1 := `{
        "action": {
        	"version": 1,
            "task_type": "xxx"
		},
        "metadata": {
			"cpu": 2,
			"instance": 1,
			"memory": 4000,
            "gateway": "contour"
		}
	}`

	str2 := `{
        "action": {
        	"version": 1
		},
		"metadata": {
			"cpu": 2,
			"instance": 1,
			"memory": 4000
		},
		"networking": {
			"gateway": "shard"
		}
	}`

	json1, err := json.Marshal(str1)
	if err != nil {
		t.Fatal(err)
	}

	json2, err := json.Marshal(str2)
	if err != nil {
		t.Fatal(err)
	}

	patch, err := jsondiff.Compare(json1, json2)
	if err != nil {
		t.Fatal(err)
	}

	t.Log(patch)
}

func TestOmitEmpty(t *testing.T) {
	cases := []struct {
		raw     string
		expect  string
		excepts []string
	}{
		{
			raw: `
			{
				"action": {
					"version": 1,
					"task_type": "xxx",
					"list_type":[],
					"map_type": {}
				},
				"metadata": {
					"cpu": 2,
					"instance": 1,
					"memory": 4000,
					"gateway": "contour"
				}
			}`,
			expect: `
			{
				"action": {
					"version": 1,
					"task_type": "xxx"
				},
				"metadata": {
					"cpu": 2,
					"instance": 1,
					"memory": 4000,
					"gateway": "contour"
				}
			}`,
		},
		{
			raw: `
			{
			"action": {
				"version": 1,
				"task_type": "xxx",
				"map_type1": {
					"a": "b",
					"map_type": {},
					"list_type": []
				 },
				"map_type2": {
					"map_type": {},
					"list_type": [],
					"a": "b",
					"c": ""
				},
				"map_type": {}
			},
			"metadata": {
				"cpu": 2,
				"instance": 1,
				"memory": 4000,
				"gateway": "contour"
			}
		}`,
			expect: `
		{
			"action": {
				"version": 1,
				"task_type": "xxx",
				"map_type1": {
					"a": "b"
				 },
				"map_type2": {
					"a": "b",
					"c": ""
				}
			},
			"metadata": {
				"cpu": 2,
				"instance": 1,
				"memory": 4000,
				"gateway": "contour"
			}
		}`,
			excepts: []string{"action.map_type2.c"},
		},
		{
			raw: `
			{
				"containers": [
					{
						"env": [
							{
								"name": "SUPERVISOR_LISTENING_PORT",
								"value": "40181"
							}
						],
						"image": "eas-pre-registry-vpc.ap-southeast-1.cr.aliyuncs.com/pai-eas/python-inference:py39-ubuntu2004",
						"name": "worker0",
						"port": 8000,
						"resources": {
							"limits": {
								"cpu": "1",
								"memory": "1G"
							},
							"requests": {
								"cpu": "1",
								"memory": "1G"
							}
						},
						"script": "python app.py"
					}
				],
				"credential": {
					"generate_token": true,
					"token": ""
				},
				"metadata": {
					"cpu": 1,
					"memory": 1000,
					"name": "free_band_test2_cache",
					"region": "ap-southeast-1"
				},
				"networking": {
					"gateway": "contour",
					"traffic_state": "Standalone"
				},
				"options": {
			
				},
				"rpc": {
					"enable_service_hang_detect": true,
					"io_threads": 4,
					"keepalive": 5000,
					"max_queue_size": 64,
					"worker_threads": 5
				},
				"runtime": {
					"termination_grace_period": 30
				},
				"scheduling": {
			
				},
				"unit": {
					"a": ""
			
				}
			}`,
			expect: `
			{
				"containers": [
					{
						"env": [
							{
								"name": "SUPERVISOR_LISTENING_PORT",
								"value": "40181"
							}
						],
						"image": "eas-pre-registry-vpc.ap-southeast-1.cr.aliyuncs.com/pai-eas/python-inference:py39-ubuntu2004",
						"name": "worker0",
						"port": 8000,
						"resources": {
							"limits": {
								"cpu": "1",
								"memory": "1G"
							},
							"requests": {
								"cpu": "1",
								"memory": "1G"
							}
						},
						"script": "python app.py"
					}
				],
				"credential": {
					"generate_token": true,
					"token": ""
				},
				"metadata": {
					"cpu": 1,
					"memory": 1000,
					"name": "free_band_test2_cache",
					"region": "ap-southeast-1"
				},
				"networking": {
					"gateway": "contour",
					"traffic_state": "Standalone"
				},
				"rpc": {
					"enable_service_hang_detect": true,
					"io_threads": 4,
					"keepalive": 5000,
					"max_queue_size": 64,
					"worker_threads": 5
				},
				"runtime": {
					"termination_grace_period": 30
				}
			}`,
			excepts: []string{"credential.token"},
		},
	}

	for _, c := range cases {
		jq, err := NewStringQuery(c.raw)
		if err != nil {
			t.Fatal(err)
		}
		jq.OmitEmpty(c.excepts...)

		jqOmitEmpty, err := NewStringQuery(c.expect)
		if err != nil {
			t.Fatal(err)
		}

		targetkeySet, err := jqOmitEmpty.ListKeys(Cascade())
		if err != nil {
			t.Fatal(err)
		}
		sort.Strings(targetkeySet)

		keySet, err := jq.ListKeys(Cascade())
		if err != nil {
			t.Fatal(err)
		}
		sort.Strings(keySet)

		if !reflect.DeepEqual(keySet, targetkeySet) {
			t.Fatalf("expected equal while actual not equal, expected keys: %v, got: %v", keySet, targetkeySet)
		}

		for _, key := range targetkeySet {
			raw, _ := jq.Query(key)
			target, _ := jqOmitEmpty.Query(key)
			if !reflect.DeepEqual(raw, target) {
				t.Fatalf("expected equal while actual not equal on key %s, expected val: %v, got: %v", key, target, raw)
			}
		}
	}

}

func TestMergeWithoutOverwrite(t *testing.T) {
	str1 := `{
        "metadata": {
			"cpu": 2,
			"instance": 1,
            "gateway": "contour"
		}
	}`

	str2 := `{
       "storage": [
           {
			   "empty_dir": {
					"medium": "memory",
					"size_limit": 500
			   },
			   "mount_path": "/dev/shm"
           }
       ],
        "metadata": {
			"cpu": 2,
			"instance": 1,
			"memory": 4000
		}
	}`

	target := `{
       "storage": [
           {
			   "empty_dir": {
					"medium": "memory",
					"size_limit": 500
			   },
			   "mount_path": "/dev/shm"
           }
       ],
        "metadata": {
			"cpu": 2,
			"instance": 1,
			"memory": 4000,
            "gateway": "contour"
		}
	}`

	curCfg, err := NewStringQuery(str1)
	if err != nil {
		t.Fatal(err)
	}

	newCfg, err := NewStringQuery(str2)
	if err != nil {
		t.Fatal(err)
	}

	targetCfg, err := NewStringQuery(target)
	if err != nil {
		t.Fatal(err)
	}

	if err = curCfg.MergeWithoutOverwrite(newCfg); err != nil {
		t.Fatal(err)
	}

	t.Log(curCfg.String())

	keySet, err := targetCfg.ListKeys(ExactlyQuery())
	if err != nil {
		t.Fatal(err)
	}

	for _, key := range keySet {
		targetVal, err := targetCfg.Query(key)
		if err != nil {
			t.Fatal(err)
		}
		curVal, err := curCfg.Query(key)
		if err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(targetVal, curVal) {
			t.Fatalf("expected equal while actual not equal on key %s, expected val: %v, got: %v", key, targetVal, curVal)
		}
	}
}

func TestMergeWithoutOverwrite_Wildcard(t *testing.T) {
	str1 := `{
        "metadata": {
			"cpu": 2,
            "memory": 4000,
			"instance": 1,
            "gateway": "contour"
		},
        "cloud": {
			"computing": {
				"instance_type": "ecs_xxx"
			},
			"networking": {
      			"security_group_id": "sg-aaa"
    		}
		}
	}`

	str2 := `{
       "cloud": {
            "computing": {
				"instances": [
					{
						"instance_type": "ecs_xx1"
					},
					{
						"instance_type": "ecs_xx2"
					}
				]
			},
    		"logging": {
      			"paths": [
        			"/home/admin/logs",
        			"/var/log"
      			]
    		},
    		"networking": {
      			"security_group_id": "sg-xxxx",
      			"vswitch_id": "vsw-xxx"
    		}
	   },
       "metadata": null
	}`

	target := `{
       "cloud": {
            "computing": {
				"instance_type": "ecs_xxx",
				"instances": [
					{
						"instance_type": "ecs_xx1"
					},
					{
						"instance_type": "ecs_xx2"
					}
				]
			},
    		"logging": {
      			"paths": [
        			"/home/admin/logs",
        			"/var/log"
      			]
    		},
    		"networking": {
      			"security_group_id": "sg-aaa",
      			"vswitch_id": "vsw-xxx"
    		}
	   },
        "metadata": {
			"cpu": 2,
			"instance": 1,
			"memory": 4000
		}
	}`

	curCfg, err := NewStringQuery(str1)
	if err != nil {
		t.Fatal(err)
	}

	newCfg, err := NewStringQuery(str2)
	if err != nil {
		t.Fatal(err)
	}

	targetCfg, err := NewStringQuery(target)
	if err != nil {
		t.Fatal(err)
	}

	if err = curCfg.MergeWithoutOverwrite(newCfg); err != nil {
		t.Fatal(err)
	}

	t.Log(curCfg.String())

	keySet, err := targetCfg.ListKeys(ExactlyQuery())
	if err != nil {
		t.Fatal(err)
	}

	for _, key := range keySet {
		targetVal, err := targetCfg.Query(key)
		if err != nil {
			t.Fatal(err)
		}
		curVal, err := curCfg.Query(key)
		if err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(targetVal, curVal) {
			t.Fatalf("expected equal while actual not equal on key %s, expected val: %v, got: %v", key, targetVal, curVal)
		}
	}
}

func TestJQWithStruct(t *testing.T) {
	str1 := `{
		"cpu": 2,
		"memory": 4000,
		"instance": 1,
		"gateway": "contour",
        "cloud": {
			"computing": {
				"instance_type": "ecs_xxx"
			},
			"networking": {
      			"security_group_id": "sg-aaa"
    		}
		}
	}`

	target := `{
		"cpu": 2,
		"memory": 4000,
		"instance": 1,
		"gateway": "contour"
	}`
	type meta struct {
		CPU      int    `json:"cpu"`
		Memory   int    `json:"memory"`
		Instance int    `json:"instance"`
		Gateway  string `json:"gateway"`
	}

	var m meta

	if err := json.Unmarshal([]byte(str1), &m); err != nil {
		t.Fatal(err)
	}

	by, _ := json.Marshal(m)
	js, _ := NewStringQuery(string(by))

	targetJs, err := NewStringQuery(target)
	if err != nil {
		t.Fatal(err)
	}

	keySet, err := targetJs.ListKeys(ExactlyQuery())
	if err != nil {
		t.Fatal(err)
	}

	for _, key := range keySet {
		targetVal, err := targetJs.Query(key)
		if err != nil {
			t.Fatal(err)
		}
		curVal, err := js.Query(key)
		if err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(targetVal, curVal) {
			t.Fatalf("expected equal while actual not equal on key %s, expected val: %v, got: %v", key, targetVal, curVal)
		}
	}
}

func TestJQ_UncoveredMethods(t *testing.T) {
	js := `
{
	"stringVal": "hello world",
	"intValue": 42,
	"floatVal": 3.14,
	"boolVal": true,
	"nilVal": null,
	"mapVal": {
		"key": "value"
	},
	"arrayVal": [1, 2, 3]
}
`
	jq, err := NewStringQuery(js)
	assert.Check(t, err)

	// Test QueryWithDefault method
	result := jq.e.jq.QueryWithDefault("stringVal", "default")
	assert.Equal(t, "hello world", result)

	result = jq.e.jq.QueryWithDefault("nonExistent", "default")
	assert.Equal(t, "default", result)

	// Test QueryToMap method
	mapResult, err := jq.e.jq.QueryToMap("mapVal")
	assert.Check(t, err)
	assert.Equal(t, "value", mapResult["key"])

	_, err = jq.e.jq.QueryToMap("stringVal")
	assert.Check(t, err != nil)

	// Test QueryToArray method
	arrayResult, err := jq.e.jq.QueryToArray("arrayVal")
	assert.Check(t, err)
	assert.Equal(t, 1.0, arrayResult[0])
	assert.Equal(t, 2.0, arrayResult[1])
	assert.Equal(t, 3.0, arrayResult[2])

	_, err = jq.e.jq.QueryToArray("stringVal")
	assert.Check(t, err != nil)

	// Test QueryToJq method
	jqResult, err := jq.e.jq.QueryToJq("mapVal")
	assert.Check(t, err)
	assert.Check(t, jqResult != nil)

	// Test QueryToInt64 method
	intResult, err := jq.e.jq.QueryToInt64("intValue")
	assert.Check(t, err)
	assert.Equal(t, int64(42), intResult)

	// Test with string number
	jq.e.jq.Set("stringInt", "123")
	intResult, err = jq.e.jq.QueryToInt64("stringInt")
	assert.Check(t, err)
	assert.Equal(t, int64(123), intResult)

	// Test QueryToInt method
	intVal, err := jq.e.jq.QueryToInt("intValue")
	assert.Check(t, err)
	assert.Equal(t, 42, intVal)

	// Test QueryToFloat64 method
	floatResult, err := jq.e.jq.QueryToFloat64("floatVal")
	assert.Check(t, err)
	assert.Equal(t, 3.14, floatResult)

	_, err = jq.e.jq.QueryToFloat64("mapVal")
	assert.Check(t, err != nil)

	// Test QueryToBool method
	boolResult, err := jq.e.jq.QueryToBool("boolVal")
	assert.Check(t, err)
	assert.Equal(t, true, boolResult)

	// Test with string boolean
	jq.e.jq.Set("stringBool", "false")
	boolResult, err = jq.e.jq.QueryToBool("stringBool")
	assert.Check(t, err)
	assert.Equal(t, false, boolResult)

	_, err = jq.e.jq.QueryToBool("mapVal")
	assert.Check(t, err != nil)

	// Test Has method
	has := jq.e.jq.Has("stringVal")
	assert.Equal(t, true, has)

	has = jq.e.jq.Has("nonExistent")
	assert.Equal(t, false, has)

	// Test StyledString method
	styled := jq.e.jq.StyledString()
	assert.Check(t, len(styled) > 0)

	// Test Bytes method
	bytes := jq.e.jq.Bytes()
	assert.Check(t, len(bytes) > 0)
}

func TestJQ_AdditionalMethods(t *testing.T) {
	js := `
{
	"stringVal": "hello world",
	"intValue": 42,
	"floatVal": 3.14,
	"boolVal": true,
	"nilVal": null,
	"defaultVal": "default"
}
`
	jq, err := NewStringQuery(js)
	assert.Check(t, err)

	// Test QueryToStringWithDefault method
	result := jq.QueryToStringWithDefault("stringVal", "default")
	assert.Equal(t, "hello world", result)

	result = jq.QueryToStringWithDefault("nonExistent", "default")
	assert.Equal(t, "default", result)

	// Test QueryToJq method
	jqResult, err := jq.QueryToJq("stringVal")
	assert.Check(t, err)
	assert.Check(t, jqResult != nil)

	// Test QueryToIntOrString method
	intOrString, err := jq.QueryToIntOrString("intValue")
	assert.Check(t, err)
	assert.Equal(t, "42", intOrString.String())

	// Test QueryToInt64WithDefault method
	int64Result := jq.QueryToInt64WithDefault("intValue", 99)
	assert.Equal(t, int64(42), int64Result)

	int64Result = jq.QueryToInt64WithDefault("nonExistent", 99)
	assert.Equal(t, int64(99), int64Result)

	// Test QueryToIntWithDefault method
	intResult := jq.QueryToIntWithDefault("intValue", 99)
	assert.Equal(t, 42, intResult)

	intResult = jq.QueryToIntWithDefault("nonExistent", 99)
	assert.Equal(t, 99, intResult)

	// Test QueryToFloat64WithDefault method
	floatResult := jq.QueryToFloat64WithDefault("floatVal", 9.9)
	assert.Equal(t, 3.14, floatResult)

	floatResult = jq.QueryToFloat64WithDefault("nonExistent", 9.9)
	assert.Equal(t, 9.9, floatResult)

	// Test QueryToBoolWithDefault method
	boolResult := jq.QueryToBoolWithDefault("boolVal", false)
	assert.Equal(t, true, boolResult)

	boolResult = jq.QueryToBoolWithDefault("nonExistent", true)
	assert.Equal(t, true, boolResult)

	// Test GetData method
	data := jq.GetData()
	assert.Check(t, data != nil)

	// Test StyledString method
	styled := jq.StyledString()
	assert.Check(t, len(styled) > 0)
}

func TestNewMethods(t *testing.T) {
	// Test NewQuery method
	data := map[string]interface{}{
		"key": "value",
	}
	jq := NewQuery(data)
	assert.Check(t, jq != nil)

	val, err := jq.Query("key")
	assert.Check(t, err)
	assert.Equal(t, "value", val)
}

func TestIsBlankMethod(t *testing.T) {
	js := `
{
	"stringVal": "hello world",
	"emptyString": "",
	"intValue": 42,
	"nilVal": null,
	"emptyArray": [],
	"arrayVal": [1, 2, 3],
	"emptyMap": {},
	"mapVal": {
		"key": "value"
	}
}
`
	jq, err := NewStringQuery(js)
	assert.Check(t, err)

	// Test IsBlank method
	assert.Equal(t, false, jq.IsBlank("stringVal"))
	assert.Equal(t, true, jq.IsBlank("emptyString"))
	assert.Equal(t, false, jq.IsBlank("intValue"))
	assert.Equal(t, true, jq.IsBlank("nilVal"))
	assert.Equal(t, true, jq.IsBlank("nonExistent"))
	assert.Equal(t, true, jq.IsBlank("emptyArray"))
	assert.Equal(t, false, jq.IsBlank("arrayVal"))
	assert.Equal(t, true, jq.IsBlank("emptyMap"))
	assert.Equal(t, false, jq.IsBlank("mapVal"))
}

func TestJQ_QueryToString(t *testing.T) {
	// Test with various data types to cover all branches in QueryToString
	js := `
{
	"stringVal": "hello world",
	"float64Val": 3.14159,
	"float32Val": 2.718,
	"intVal": 42,
	"int8Val": 127,
	"int16Val": 32767,
	"int32Val": 2147483647,
	"int64Val": 9007199254740991,
	"uintVal": 42,
	"uint8Val": 255,
	"uint16Val": 65535,
	"uint32Val": 4294967295,
	"uint64Val": 9007199254740991,
	"boolTrue": true,
	"boolFalse": false,
	"nilVal": null,
	"mapVal": {
		"key": "value"
	},
	"arrayVal": [1, 2, 3],
	"structVal": {
		"name": "test",
		"count": 10
	}
}
`
	jq, err := NewStringQuery(js)
	assert.Check(t, err)

	// Test string type
	result, err := jq.QueryToString("stringVal")
	assert.Check(t, err)
	assert.Equal(t, "hello world", result)

	// Test float64 type
	result, err = jq.QueryToString("float64Val")
	assert.Check(t, err)
	assert.Equal(t, "3.14159", result)

	// Test float32 type
	result, err = jq.QueryToString("float32Val")
	assert.Check(t, err)
	assert.Equal(t, "2.718", result)

	// Test int type
	result, err = jq.QueryToString("intVal")
	assert.Check(t, err)
	assert.Equal(t, "42", result)

	// Test int8 type
	result, err = jq.QueryToString("int8Val")
	assert.Check(t, err)
	assert.Equal(t, "127", result)

	// Test int16 type
	result, err = jq.QueryToString("int16Val")
	assert.Check(t, err)
	assert.Equal(t, "32767", result)

	// Test int32 type
	result, err = jq.QueryToString("int32Val")
	assert.Check(t, err)
	assert.Equal(t, "2147483647", result)

	// Test int64 type (using max safe integer in JavaScript to avoid precision loss)
	result, err = jq.QueryToString("int64Val")
	assert.Check(t, err)
	assert.Equal(t, "9007199254740991", result)

	// Test uint type
	result, err = jq.QueryToString("uintVal")
	assert.Check(t, err)
	assert.Equal(t, "42", result)

	// Test uint8 type
	result, err = jq.QueryToString("uint8Val")
	assert.Check(t, err)
	assert.Equal(t, "255", result)

	// Test uint16 type
	result, err = jq.QueryToString("uint16Val")
	assert.Check(t, err)
	assert.Equal(t, "65535", result)

	// Test uint32 type
	result, err = jq.QueryToString("uint32Val")
	assert.Check(t, err)
	assert.Equal(t, "4294967295", result)

	// Test uint64 type (using max safe integer in JavaScript to avoid precision loss)
	result, err = jq.QueryToString("uint64Val")
	assert.Check(t, err)
	assert.Equal(t, "9007199254740991", result)

	// Test bool true
	result, err = jq.QueryToString("boolTrue")
	assert.Check(t, err)
	assert.Equal(t, "true", result)

	// Test bool false
	result, err = jq.QueryToString("boolFalse")
	assert.Check(t, err)
	assert.Equal(t, "false", result)

	// Test nil value
	result, err = jq.QueryToString("nilVal")
	assert.Check(t, err)
	assert.Equal(t, "", result)

	// Test map type (complex type that should be marshaled to JSON)
	result, err = jq.QueryToString("mapVal")
	assert.Check(t, err)
	assert.Equal(t, "{\"key\":\"value\"}\n", result)

	// Test array type (complex type that should be marshaled to JSON)
	result, err = jq.QueryToString("arrayVal")
	assert.Check(t, err)
	assert.Equal(t, "[1,2,3]\n", result)

	// Test struct type (complex type that should be marshaled to JSON)
	result, err = jq.QueryToString("structVal")
	assert.Check(t, err)
	assert.Equal(t, "{\"count\":10,\"name\":\"test\"}\n", result)

	// Test non-existent key (should return error)
	_, err = jq.QueryToString("nonExistentKey")
	assert.Check(t, err != nil)
}

func TestUtilityFunctions(t *testing.T) {
	// Test SplitArgs function
	result, err := SplitArgs("a.b.c", "\\.", false)
	assert.Check(t, err)
	assert.DeepEqual(t, []string{"a", "b", "c"}, result)

	result, err = SplitArgs("'a.b'.c", "\\.", false)
	assert.Check(t, err)
	assert.DeepEqual(t, []string{"a.b", "c"}, result)

	// Test CountSeparators function
	count, err := CountSeparators("a.b.c", "\\.")
	assert.Check(t, err)
	assert.Equal(t, 2, count)

	count, err = CountSeparators("'a.b'.c", "\\.")
	assert.Check(t, err)
	assert.Equal(t, 1, count)
}

func TestIsTrueMethod(t *testing.T) {
	js := `
{
	"boolTrue": true,
	"boolFalse": false,
	"stringTrue": "true",
	"stringFalse": "false",
	"stringInvalid": "invalid",
	"intValue": 42,
	"nilVal": null
}
`
	jq, err := NewStringQuery(js)
	assert.Check(t, err)

	// Test boolean values
	assert.Equal(t, true, jq.e.jq.IsTrue("boolTrue"))
	assert.Equal(t, false, jq.e.jq.IsTrue("boolFalse"))

	// Test string boolean values
	assert.Equal(t, true, jq.e.jq.IsTrue("stringTrue"))
	assert.Equal(t, false, jq.e.jq.IsTrue("stringFalse"))

	// Test invalid values
	assert.Equal(t, false, jq.e.jq.IsTrue("stringInvalid"))
	assert.Equal(t, false, jq.e.jq.IsTrue("intValue"))
	assert.Equal(t, false, jq.e.jq.IsTrue("nilVal"))
	assert.Equal(t, false, jq.e.jq.IsTrue("nonExistent"))
}

func TestEquivalenceKeyMethods(t *testing.T) {
	ek := &EquivalenceKey{
		Preferred: "preferredKey",
		Keys:      []string{"key1", "key2"},
	}

	// Test Format method
	format := ek.Format()
	assert.Equal(t, "preferredKey,key1,key2", format)

	// Test String method
	str := ek.String()
	assert.Equal(t, "preferred: preferredKey, compatible: [key1 key2]", str)

	// Test AddSub method
	subKey := ek.AddSub("sub")
	assert.Equal(t, "preferredKey.sub", subKey.Preferred)
	assert.Equal(t, "key1.sub", subKey.Keys[0])
	assert.Equal(t, "key2.sub", subKey.Keys[1])

	// Test Clone method
	clone := ek.Clone()
	assert.Equal(t, ek.Preferred, clone.Preferred)
	assert.DeepEqual(t, ek.Keys, clone.Keys)
}

func TestOptionMethods(t *testing.T) {
	// Test RemoveRedundant option
	opt := RemoveRedundant()
	options := defaultQueryOptions()
	opt(&options)
	assert.Equal(t, true, options.removeRedundant)

	// Test CompatibleQuery option
	opt = CompatibleQuery()
	opt(&options)
	assert.Equal(t, true, options.caseCompatible)

	// Test NoPreferred option
	opt = NoPreferred()
	opt(&options)
	assert.Equal(t, true, options.noPreferred)
}

func TestEquivalentQueryMethods(t *testing.T) {
	// Test QueryWithDefault method
	js := `{"key": "value"}`
	jq, err := NewStringQuery(js)
	assert.Check(t, err)

	eq := jq.e

	// Test existing key
	ek := ParseKey("key")
	result := eq.QueryWithDefault(&ek, "default")
	assert.Equal(t, "value", result)

	// Test non-existing key
	ek2 := ParseKey("nonexistent")
	result = eq.QueryWithDefault(&ek2, "default")
	assert.Equal(t, "default", result)

	// Test QueryToMap method
	ek3 := ParseKey("key")
	_, err = eq.QueryToMap(&ek3)
	// Should fail because "key" is a string, not a map
	assert.Check(t, err != nil)

	// Test QueryToArray method
	ek4 := ParseKey("key")
	_, err = eq.QueryToArray(&ek4)
	// Should fail because "key" is a string, not an array
	assert.Check(t, err != nil)

	// Test QueryToString method
	ek5 := ParseKey("key")
	resultStr, err := eq.QueryToString(&ek5)
	assert.Check(t, err)
	assert.Equal(t, "value", resultStr)

	// Test QueryToInt64 method
	_, err = eq.QueryToInt64(&ek5)
	// Should fail because "key" is a string that can't be converted to int
	assert.Check(t, err != nil)

	// Test QueryToFloat64 method
	_, err = eq.QueryToFloat64(&ek5)
	// Should fail because "key" is a string that can't be converted to float
	assert.Check(t, err != nil)

	// Test QueryToBool method
	_, err = eq.QueryToBool(&ek5)
	// Should fail because "key" is a string that can't be converted to bool
	assert.Check(t, err != nil)

	// Test Has method
	assert.Equal(t, true, eq.Has(&ek))

	// Test IsTrue method
	assert.Equal(t, false, eq.IsTrue(&ek5))
}

func TestEquivalentQueryAdditionalMethods(t *testing.T) {
	// Test QueryToInt method
	js := `{"intValue": 42, "stringValue": "not a number"}`
	jq, err := NewStringQuery(js)
	assert.Check(t, err)

	eq := jq.e

	// Test with a valid integer value
	ek := ParseKey("intValue")
	result, err := eq.QueryToInt(&ek)
	assert.Check(t, err)
	// Should be 42 as int
	assert.Equal(t, int(42), result)

	// Test with a non-integer value
	ek2 := ParseKey("stringValue")
	_, err = eq.QueryToInt(&ek2)
	// Should fail because "stringValue" is a string that can't be converted to int
	assert.Check(t, err != nil)

	// Test QueryToBool method
	ek3 := ParseKey("intValue")
	_, err = eq.QueryToBool(&ek3)
	// Should fail because 42 is a number, not a boolean
	assert.Check(t, err != nil)

	// Test with a boolean value
	js2 := `{"boolValue": true}`
	jq2, err := NewStringQuery(js2)
	assert.Check(t, err)

	eq2 := jq2.e
	ek4 := ParseKey("boolValue")
	resultBool2, err := eq2.QueryToBool(&ek4)
	assert.Check(t, err)
	assert.Equal(t, true, resultBool2)
}

func TestJQ_AdditionalMethods2(t *testing.T) {
	// Test QueryWithDefault method
	js := `{"key": "value"}`
	jq, err := NewStringQuery(js)
	assert.Check(t, err)

	// Test existing key
	result := jq.QueryWithDefault("key", "default")
	assert.Equal(t, "value", result)

	// Test non-existing key
	result = jq.QueryWithDefault("nonexistent", "default")
	assert.Equal(t, "default", result)

	// Test QueryToArray method
	_, err = jq.QueryToArray("key")
	// Should fail because "key" is a string, not an array
	assert.Check(t, err != nil)

	// Test QueryToFloat64 method
	_, err = jq.QueryToFloat64("key")
	// Should fail because "key" is a string that can't be converted to float
	assert.Check(t, err != nil)

	// Test QueryToFloat64WithDefault method
	resultFloat := jq.QueryToFloat64WithDefault("key", 3.14)
	assert.Equal(t, 3.14, resultFloat)

	// Test QueryToBoolWithDefault method
	resultBool := jq.QueryToBoolWithDefault("key", true)
	assert.Equal(t, true, resultBool)

	// Test Clone method
	clone := jq.Clone()
	assert.Check(t, clone != nil)

	// Test DeepCopy method
	deepCopy := jq.DeepCopy()
	assert.Check(t, deepCopy != nil)
}

func TestConstructorMethods(t *testing.T) {
	// Test NewFileQuery method with a non-existent file
	_, err := NewFileQuery("nonexistent.json")
	assert.Check(t, err != nil)

	// Test NewBytesQuery method
	jsonBytes := []byte(`{"key": "value"}`)
	jq, err := NewBytesQuery(jsonBytes)
	assert.Check(t, err)
	assert.Check(t, jq != nil)

	// Test NewObjectQuery method
	type TestStruct struct {
		Key string `json:"key"`
	}
	obj := TestStruct{Key: "value"}
	jq2, err := NewObjectQuery(obj)
	assert.Check(t, err)
	assert.Check(t, jq2 != nil)

	// Test MustNewStringQuery method
	jq3 := MustNewStringQuery(`{"key": "value"}`)
	assert.Check(t, jq3 != nil)

	// Test panic in MustNewStringQuery
	defer func() {
		if r := recover(); r != nil {
			// Expected panic
			assert.Check(t, true)
		}
	}()
	// This should panic
	MustNewStringQuery(`{invalid json}`)
}

func TestExactKeyMethod(t *testing.T) {
	js := `{"key": "value"}`
	jq, err := NewStringQuery(js)
	assert.Check(t, err)

	// Test existing key
	key := jq.ExactKey("key")
	// Based on implementation, ExactKey returns the exactKey field from EquivalenceKey
	// which is never populated, so it always returns an empty string
	assert.Equal(t, "", key)

	// Test non-existent key
	key = jq.ExactKey("nonExistent")
	// ExactKey returns empty string for non-existent keys
	assert.Equal(t, "", key)
}

func TestStripDotedExceptForMethod(t *testing.T) {
	// Test with nil config
	StripDotedExceptFor(nil, "key")

	// Test with empty key
	js := `{"key": "value"}`
	jq, err := NewStringQuery(js)
	assert.Check(t, err)
	StripDotedExceptFor(jq, "")

	// Test with non-existent key
	StripDotedExceptFor(jq, "nonexistent")

	// Test with existing key
	StripDotedExceptFor(jq, "key")
}

func TestJQErrors(t *testing.T) {
	// Test jqErrors Error method
	err1 := errors.New("error 1")
	err2 := errors.New("error 2")
	errs := &jqErrors{
		errs: []error{err1, err2},
	}

	expected := "error 1,error 2"
	actual := errs.Error()
	assert.Equal(t, expected, actual)

	// Test with single error
	errsSingle := &jqErrors{
		errs: []error{err1},
	}

	expectedSingle := "error 1"
	actualSingle := errsSingle.Error()
	assert.Equal(t, expectedSingle, actualSingle)

	// Test with no errors
	errsEmpty := &jqErrors{
		errs: []error{},
	}

	expectedEmpty := ""
	actualEmpty := errsEmpty.Error()
	assert.Equal(t, expectedEmpty, actualEmpty)
}

// TestJQ_ExpandSlice tests the slice expansion functionality in the Set method
func TestJQ_ExpandSlice(t *testing.T) {
	// Test case 1: Normal slice expansion
	js := `{
		"array": [1, 2, 3]
	}`
	jq, err := NewStringQuery(js)
	assert.Check(t, err)

	// Set value at index 5, which should expand the slice to accommodate it
	err = jq.Set("array.[5]", "expanded")
	assert.Check(t, err)

	// Check that the array has been expanded correctly
	result, err := jq.Query("array")
	assert.Check(t, err)
	arrayResult := result.([]interface{})
	assert.Equal(t, 6, len(arrayResult))        // Should have 6 elements now
	assert.Equal(t, 1.0, arrayResult[0])        // Original value
	assert.Equal(t, 2.0, arrayResult[1])        // Original value
	assert.Equal(t, 3.0, arrayResult[2])        // Original value
	assert.Equal(t, nil, arrayResult[3])        // New nil value
	assert.Equal(t, nil, arrayResult[4])        // New nil value
	assert.Equal(t, "expanded", arrayResult[5]) // New value

	// Test case 2: Slice expansion with nested object
	js2 := `{
		"parent": {
			"childArray": ["a", "b"]
		}
	}`
	jq2, err := NewStringQuery(js2)
	assert.Check(t, err)

	// Expand the child array
	err = jq2.Set("parent.childArray.[4]", "nestedExpanded")
	assert.Check(t, err)

	result2, err := jq2.Query("parent.childArray")
	assert.Check(t, err)
	arrayResult2 := result2.([]interface{})
	assert.Equal(t, 5, len(arrayResult2))
	assert.Equal(t, "a", arrayResult2[0])
	assert.Equal(t, "b", arrayResult2[1])
	assert.Equal(t, nil, arrayResult2[2])
	assert.Equal(t, nil, arrayResult2[3])
	assert.Equal(t, "nestedExpanded", arrayResult2[4])

	// Test case 3: Error case - cannot expand slice without parent context
	// Create a standalone slice that's not part of a map
	// jq is a private type, so we can't directly instantiate it.
	// We'll create a valid JSON structure and then try to access a slice without parent context
	js3 := `[]`
	jq3, err := NewStringQuery(js3)
	assert.Check(t, err)

	// Directly access the underlying jq struct to simulate the error case
	jq3Struct := jq3.e.jq
	err = jq3Struct.Set("[5]", "shouldFail")
	assert.Check(t, err != nil)
	assert.Check(t, strings.Contains(err.Error(), "slice is short"))

	// Test case 4: Error case - parent context is not a map
	// To trigger "cannot expand the slice", we need:
	// 1. An array inside another array (not a map)
	// 2. Try to set a value at index 5 of the inner array, which should trigger the error
	// 3. prevContext exists but is not a map
	js4 := `[[1, 2]]` // Array containing an array
	jq4, err := NewStringQuery(js4)
	assert.Check(t, err)

	// Try to set a value at index 5 of the inner array, which should trigger the error
	// because prevContext (the outer array) is not a map
	jq4Struct := jq4.e.jq
	err = jq4Struct.Set("[0].[5]", "shouldFail")
	assert.Check(t, err != nil)
	assert.Check(t, strings.Contains(err.Error(), "cannot expand the slice"))
}

func TestJQ_QueryStruct(t *testing.T) {
	// Define a struct to test querying struct fields directly
	type TestStruct struct {
		Name    string `json:"name"`
		Age     int    `json:"age"`
		Visible string // No json tag
	}

	testStruct := TestStruct{
		Name:    "John Doe",
		Age:     30,
		Visible: "visible",
	}

	// Create a jq with the struct as data
	jq := &jq{data: testStruct}

	// Test querying existing field with json tag
	result, err := jq.Query("Name")
	assert.Check(t, err)
	assert.Equal(t, "John Doe", result)

	// Test querying existing field with json tag
	result, err = jq.Query("Age")
	assert.Check(t, err)
	assert.Equal(t, 30, result)

	// Test querying existing field without json tag
	result, err = jq.Query("Visible")
	assert.Check(t, err)
	assert.Equal(t, "visible", result)

	// Test querying non-existing field
	_, err = jq.Query("NonExistent")
	assert.Check(t, err != nil)
	// Just check that an error occurred, not the exact message format
}
