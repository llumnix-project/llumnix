package gron

import (
	"bufio"
	"bytes"
	"fmt"
	"io"
	"io/ioutil"
	"os"
	"reflect"
	"testing"

	"k8s.io/klog/v2"
)

func TestUnGronHelper(t *testing.T) {
	for _, testCase := range []struct {
		sourceFilePath string
		targetFilePath string
	}{
		{
			sourceFilePath: "./testdata/a.kv",
			targetFilePath: "./testdata/a.gron",
		},
		{
			sourceFilePath: "./testdata/b.kv",
			targetFilePath: "./testdata/b.gron",
		},
	} {
		kvs, err := readHelper(testCase.sourceFilePath)
		if err != nil {
			t.Fatal(err)
		}
		gron := UngronHelper(kvs)
		have := []byte(gron)

		klog.Infof("\n%s", gron)

		want, err := ioutil.ReadFile(testCase.targetFilePath)
		if err != nil {
			t.Fatalf("failed to open want file: %s", err)
		}

		if !reflect.DeepEqual(want, have) {
			t.Logf("want: %#v", string(want))
			t.Logf("have: %#v", string(have))
			t.Errorf("UnGronHelper %s does not match %s", testCase.sourceFilePath, testCase.targetFilePath)
		}
	}
}

func TestUnGronWithHelper(t *testing.T) {
	for _, testCase := range []struct {
		sourceFilePath string
		targetFilePath string
	}{
		{
			sourceFilePath: "./testdata/a.kv",
			targetFilePath: "./testdata/a.json",
		},
		{
			sourceFilePath: "./testdata/b.kv",
			targetFilePath: "./testdata/b.json",
		},
		{
			sourceFilePath: "./testdata/c.kv",
			targetFilePath: "./testdata/c.json",
		},
	} {
		kvs, err := readHelper(testCase.sourceFilePath)
		if err != nil {
			t.Fatal(err)
		}
		gron := UngronHelper(kvs)
		br := bytes.NewBufferString(gron)

		out := &bytes.Buffer{}

		if _, err := Ungron(br, out, OptMonochrome); err != nil {
			t.Fatalf("ungronWithHelper %s to %s failed: %v", testCase.sourceFilePath, testCase.targetFilePath, err)
		}

		klog.Infof("\n%s", out.String())
	}
}

func readHelper(file string) ([]string, error) {
	in, err := os.Open(file)
	if err != nil {
		return nil, fmt.Errorf("failed to open source file %s: %v", file, err)
	}
	defer in.Close()

	var kvs []string
	br := bufio.NewReader(in)
	for {
		line, _, err := br.ReadLine()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("failed to read line: %v", err)
		}
		if len(line) > 0 {
			kvs = append(kvs, string(line))
		}
	}
	return kvs, nil
}
