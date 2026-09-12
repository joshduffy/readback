package registry

import "testing"

func TestRegisterAndSearch(t *testing.T) {
	Register(Module{Name: "zz-test", Summary: "Proves a claim against GitHub", Status: StatusStub, Keywords: []string{"claim"}})
	if _, ok := Get("zz-test"); !ok {
		t.Fatal("registered module not found")
	}
	if got := Search("CLAIM"); len(got) == 0 || got[len(got)-1].Name != "zz-test" {
		t.Fatalf("search miss: %+v", got)
	}
	defer func() {
		if recover() == nil {
			t.Fatal("duplicate register did not panic")
		}
	}()
	Register(Module{Name: "zz-test"})
}
