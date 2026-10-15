package api

import (
	"errors"
	"testing"

	"github.com/DATASMARTCOMCORP/traccar-api-java/server-go/internal/model"
)

// The harness checks request bodies end to end against Java (suites crud and write);
// these cover the reading rules directly.

func TestDecodeDeviceJacksonRules(t *testing.T) {
	var d model.Device
	err := decodeBean([]byte(`{"id":"7","name":"A","uniqueId":"  U-1  ","phone":" 12 ","groupId":3.9,
		"disabled":"true","motionState":true,"name":"B","attributes":{"x":1.0}}`), deviceBean(&d))
	if err != nil {
		t.Fatal(err)
	}
	if d.ID != 7 || d.UniqueID != "U-1" || *d.Phone != "12" || d.GroupID != 3 || !d.Disabled || d.Name != "B" {
		t.Errorf("device = %+v", d)
	}
	if d.Attributes.String() != `{"x":1.0}` {
		t.Errorf("attributes %s", d.Attributes.String())
	}
}

func TestDecodeErrors(t *testing.T) {
	var d model.Device
	cases := map[string]string{
		"unknown property":        `{"name":"A","uniqueId":"U","colour":"red"}`,
		"null in trimming setter": `{"name":"A","uniqueId":null}`,
		"object for a number":     `{"name":"A","uniqueId":"U","groupId":{}}`,
		"number out of range":     `{"name":"A","uniqueId":"U","groupId":1e30}`,
		"not an object":           `[1]`,
		"trailing data":           `{"name":"A","uniqueId":"U"} {}`,
	}
	for name, body := range cases {
		if err := decodeBean([]byte(body), deviceBean(&d)); err == nil {
			t.Errorf("%s: want error", name)
		}
	}
	var missing *errNotNull
	if err := decodeBean([]byte(`{"name":"A"}`), deviceBean(&d)); !errors.As(err, &missing) || missing.property != "uniqueId" {
		t.Errorf("absent NOT NULL property: %v", err)
	}
	var s model.ServerInfo
	if err := decodeBean([]byte(`{"id":1,"version":"x","whatever":[1,2]}`), serverBean(&s)); err != nil {
		t.Errorf("Server ignores unknown properties: %v", err)
	}
}

func TestDecodeScalarCoercions(t *testing.T) {
	var u model.User
	err := decodeBean([]byte(`{"name":"N","email":" e@x ","zoom":"","deviceLimit":null,"latitude":"1.5",
		"readonly":1,"password":"pw","totpKey":123}`), userBean(&u))
	if err != nil {
		t.Fatal(err)
	}
	if u.Email != "e@x" || u.Zoom != 0 || u.DeviceLimit != 0 || u.Latitude != 1.5 || !u.Readonly || *u.TotpKey != "123" {
		t.Errorf("user = %+v", u)
	}
	if u.HashedPassword == nil || u.Salt == nil {
		t.Error("password must be hashed into hash and salt")
	}
	var empty model.User
	if err := decodeBean([]byte(`{"name":"N","email":"e","password":""}`), userBean(&empty)); err != nil || empty.HashedPassword != nil {
		t.Errorf("empty password changes nothing: %v %v", err, empty.HashedPassword)
	}
}
