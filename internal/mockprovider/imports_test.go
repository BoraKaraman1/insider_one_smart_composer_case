package mockprovider_test

import (
	"go/build"
	"strings"
	"testing"
)

const module = "github.com/BoraKaraman1/insider_one_smart_composer_case"

func TestImportsNoAppPackage(t *testing.T) {
	pkg, err := build.ImportDir(".", 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(pkg.GoFiles) == 0 {
		t.Fatal("no non-test Go files found; the check would pass vacuously")
	}
	for _, imp := range pkg.Imports {
		if imp == module || strings.HasPrefix(imp, module+"/") {
			t.Errorf("mockprovider imports app package %s", imp)
		}
	}
}
