package json

import (
	"testing"
)

type TestStruct struct {
	A string `json:"a"`

	Bb string `json:"ccccc"`
}

type TestStruct2 struct {
	D []TestStruct `json:"d"`

	Cdf string `json:"cdf"`
}

func TestCodec(t *testing.T) {
	marshal := NewExtendedJSON()

	data, err := marshal.MarshallJSON(TestStruct{"abc", "cde"})
	if err != nil {
		t.Error(err)
		t.FailNow()
	}

	t.Log(string(data))

	data, err = marshal.MarshallJSON(TestStruct2{D: []TestStruct{{"abc", "cde"}}, Cdf: "abc"})
	if err != nil {
		t.Error(err)
		t.FailNow()
	}

	t.Log(string(data))
}

func TestCamelCompatible(t *testing.T) {
	json := NewExtendedJSON()
	s := struct {
		Field1 string `json:"field_foo"`
		Field2 string `json:"fieldOk"`
	}{}

	data := `
{
	"fieldFoo": "foo1",
	"fieldOk": "bar1",
	"field_foo": "foo",
	"field_ok": "bar"
}
`
	if err := json.Unmarshal([]byte(data), &s); err != nil {
		t.Fatal(err)
	}

	if s.Field1 != "foo" {
		t.Errorf("unexpected %s", s.Field1)
	}

	if s.Field2 != "bar" {
		t.Errorf("unexpected %s", s.Field2)
	}
}

func TestEncoderOrder(t *testing.T) {
	json := NewExtendedJSON()
	s := struct {
		Field1 string `json:"fieldFoo"`
		Field2 string `json:"fieldBar"`
	}{}

	data := `
{
	"field_foo": "foo",
	"field_bar": "bar"
}
`
	if err := json.Unmarshal([]byte(data), &s); err != nil {
		t.Fatal(err)
	}

	if s.Field1 != "foo" {
		t.Errorf("unexpected %s", s.Field1)
	}

	if s.Field2 != "bar" {
		t.Errorf("unexpected %s", s.Field2)
	}

	output, err := json.Marshal(s)
	if err != nil {
		t.Fatal(err)
	}

	t.Log(string(output))
}
