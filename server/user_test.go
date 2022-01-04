package server

import (
	"reflect"
	"testing"

	"github.com/isrc-cas/gt/config"
)

func TestUser(t *testing.T) {
	args := []string{
		"server",
		"-config", "./testdata/config.yaml",
		"-users", "./testdata/users.yaml",
		"-id", "id1",
		"-secret", "secret1-overwrite-overwrite",
		"-id", "id5",
		"-secret", "secret5",
	}
	conf := Config{}
	err := config.ParseFlags(args, &conf, &conf.Options)
	if err != nil {
		t.Fatal(err)
	}
	usersYaml := struct{ Users Users }{make(Users)}
	err = config.Yaml2Interface(&conf.Options.Users, &usersYaml)
	if err != nil {
		t.Fatal(err)
	}
	err = conf.Users.mergeUsers(usersYaml.Users, conf.ID, conf.Secret)
	if err != nil {
		t.Fatal(err)
	}

	expectedResult := Users{
		"id1": {
			Secret: "secret1-overwrite-overwrite",
		},
		"id2": {
			Secret: "secret2-overwrite",
		},
		"id3": {
			Secret: "secret3",
		},
		"id4": {
			Secret: "secret4",
		},
		"id5": {
			Secret: "secret5",
		},
	}
	if !reflect.DeepEqual(&expectedResult, &conf.Users) {
		t.Fatal("unexpected result")
	}
}
