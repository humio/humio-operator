package controllers

import (
	"errors"
	"golang.org/x/exp/constraints"
	"reflect"
	"testing"
)

type genericMapTestCase[K comparable, V constraints.Ordered] struct {
	name        string
	input       map[K]V
	expectedKey K
	error       error
}

func processGenericMapTestCase[K comparable, V constraints.Ordered](t *testing.T, tests []genericMapTestCase[K, V]) {
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			key, _ := GetKeyWithHighestValue(test.input)
			if !reflect.DeepEqual(test.expectedKey, key) {
				t.Errorf("Expected key: %v, got: %v", test.expectedKey, key)
			}
		})
	}
}

func TestGetKeyWithHighestValue(t *testing.T) {
	stringIntTests := []genericMapTestCase[string, int]{
		{
			name:        "Non-empty map",
			input:       map[string]int{"a": 23, "b": 42, "c": 13},
			expectedKey: "b",
			error:       nil,
		},
		{
			name:        "Empty map",
			input:       map[string]int{},
			expectedKey: "",
			error:       errors.New("map is empty"),
		},
		{
			name:        "Map with one entry",
			input:       map[string]int{"a": 55},
			expectedKey: "a",
			error:       nil,
		},
		{
			name:        "Map with multiple keys having the same value",
			input:       map[string]int{"a": 44, "b": 44, "c": 13, "d": 22},
			expectedKey: "a",
			error:       nil,
		},
	}

	intFloat := []genericMapTestCase[int, float64]{
		{
			name:        "Non-empty int-float map",
			input:       map[int]float64{12: 23.2, 1: 42.1, 7: 13.99},
			expectedKey: 1,
			error:       nil,
		},
		{
			name:        "Empty int-float map",
			input:       map[int]float64{},
			expectedKey: 0,
			error:       errors.New("map is empty"),
		},
	}

	processGenericMapTestCase(t, stringIntTests)
	processGenericMapTestCase(t, intFloat)
}
