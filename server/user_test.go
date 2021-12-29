package server

import (
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
	u := make(map[string]user)
	err = config.Yaml2Interface(conf.Options.Users, u)
	if err != nil {
		t.Fatal(err)
	}
	result := users{}

	err = result.mergeUsers(conf.Users, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	err = result.mergeUsers(u, conf.ID, conf.Secret)
	if err != nil {
		t.Fatal(err)
	}

	expectedResult := users{}
	expectedResult.Store("id1", user{Secret: "secret1-overwrite-overwrite"})
	expectedResult.Store("id2", user{Secret: "secret2-overwrite"})
	expectedResult.Store("id3", user{Secret: "secret3"})
	expectedResult.Store("id4", user{Secret: "secret4"})
	expectedResult.Store("id5", user{Secret: "secret5"})
	expectedResult.Range(func(key, value interface{}) bool {
		v, ok := result.Load(key)
		if !ok {
			t.Fatalf("%q does not exist", key)
		}
		if value.(user).Secret != v.(user).Secret {
			t.Fatal("secret does not match")
		}
		return true
	})
}
