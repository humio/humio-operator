package helpers

import (
	"reflect"

	humioapi "github.com/humio/cli/api"
)

func GetTypeName(myvar interface{}) string {
	t := reflect.TypeOf(myvar)
	if t.Kind() == reflect.Ptr {
		return t.Elem().Name()
	}
	return t.Name()
}

func ContainsElement(list []string, s string) bool {
	for _, v := range list {
		if v == s {
			return true
		}
	}
	return false
}

func RemoveElement(list []string, s string) []string {
	for i, v := range list {
		if v == s {
			list = append(list[:i], list[i+1:]...)
		}
	}
	return list
}

// TODO: refactor, this is copied from the humio/cli/api/parsers.go
func MapTests(vs []string, f func(string) humioapi.ParserTestCase) []humioapi.ParserTestCase {
	vsm := make([]humioapi.ParserTestCase, len(vs))
	for i, v := range vs {
		vsm[i] = f(v)
	}
	return vsm
}

// TODO: refactor, this is copied from the humio/cli/api/parsers.go
func ToTestCase(line string) humioapi.ParserTestCase {
	return humioapi.ParserTestCase{
		Input:  line,
		Output: map[string]string{},
	}
}
