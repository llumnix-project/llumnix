package gron

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io/ioutil"
	"k8s.io/klog/v2"
	"reflect"
	"testing"
)

func TestGron(t *testing.T) {
	for _, testCase := range []struct {
		sourceFilePath string
		targetFilePath string
	}{
		{
			sourceFilePath: "./testdata/a.json",
			targetFilePath: "./testdata/a.kv",
		},
		{
			sourceFilePath: "./testdata/b.json",
			targetFilePath: "./testdata/b.kv",
		},
		{
			sourceFilePath: "./testdata/c.json",
			targetFilePath: "./testdata/c.kv",
		},
	} {
		in, err := ioutil.ReadFile(testCase.sourceFilePath)
		if err != nil {
			t.Fatalf("failed to read source file: %v", err)
		}
		br := bytes.NewBuffer(in)
		stats, err := Gron(br, OptMonochrome)
		out := &bytes.Buffer{}

		for _, stat := range Simplify(stats) {
			out.WriteString(fmt.Sprintf("%s\n", stat))
		}

		klog.Infof("\n%s", out.String())

		have := out.Bytes()

		want, err := ioutil.ReadFile(testCase.targetFilePath)
		if err != nil {
			t.Fatal(err)
		}

		if !reflect.DeepEqual(want, have) {
			t.Logf("want: %v", string(want))
			t.Logf("have: %v", string(have))
			t.Errorf("Gron %s does not match %s", testCase.sourceFilePath, testCase.targetFilePath)
		}
	}
}

func TestJsonOmitEmpty(t *testing.T) {
	type Student struct {
		Name     string   `json:"name,omitempty"`
		Friends  []string `json:"friends,omitempty"`
		Families []string `json:"friends"`
		School   string   `json:"school"`
		Gender   string   `json:"gender"`
		Hobbies  []string `json:"hobbies"`
		Age      int      `json:"age"`
	}

	s := Student{
		Gender:  "male",
		Hobbies: []string{"swimming", "golf"},
	}

	by, err := json.Marshal(s)
	if err != nil {
		t.Fatal(err)
	}
	klog.Info(string(by))

	js := `{"gender": "male", "age":13}`
	s2 := Student{}
	if err := json.Unmarshal([]byte(js), &s2); err != nil {
		t.Fatal(err)
	}
	klog.Infof("name:%v, gender:%v, hobby:%v, age:%v", s2.Name, s2.Gender, s2.Hobbies, s2.Age)
}

func TestUnGron(t *testing.T) {
	for _, testCase := range []struct {
		sourceFilePath string
		targetFilePath string
	}{
		{
			sourceFilePath: "./testdata/a.gron",
			targetFilePath: "./testdata/a.json",
		},
		{
			sourceFilePath: "./testdata/b.gron",
			targetFilePath: "./testdata/b.json",
		},
		{
			sourceFilePath: "./testdata/c.gron",
			targetFilePath: "./testdata/c.json",
		},
	} {
		in, err := ioutil.ReadFile(testCase.sourceFilePath)
		if err != nil {
			t.Fatalf("failed to open source file: %v", err)
		}
		br := bytes.NewBuffer(in)

		out := &bytes.Buffer{}
		if _, err := Ungron(br, out, OptMonochrome); err != nil {
			t.Fatalf("ungron %s to %s failed: %v", testCase.sourceFilePath, testCase.targetFilePath, err)
		}

		klog.Infof("\n%s", out.String())

		var have, want interface{}
		err = json.Unmarshal(out.Bytes(), &have)
		if err != nil {
			t.Fatalf("failed to load have file: %v", err)
		}

		wantF, err := ioutil.ReadFile(testCase.targetFilePath)
		if err != nil {
			t.Fatalf("failed to load target file: %v", err)
		}

		err = json.Unmarshal(wantF, &want)
		if err != nil {
			t.Fatalf("failed to unmarshal target file: %v", err)
		}

		if !reflect.DeepEqual(have, want) {
			if !reflect.DeepEqual(want, have) {
				t.Logf("want: %v", want)
				t.Logf("have: %v", have)
				t.Errorf("Gron %s does not match %s", testCase.sourceFilePath, testCase.targetFilePath)
			}
		}
	}
}
