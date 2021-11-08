package controllers

import (
	"testing"

	humioapi "github.com/humio/cli/api"
)

func TestViewConnectionsDiffer(t *testing.T) {
	tt := []struct {
		name         string
		current, new []humioapi.ViewConnection
		differ       bool
	}{
		{
			name:    "nil connections",
			current: nil,
			new:     nil,
			differ:  false,
		},
		{
			name:    "empty slices",
			current: []humioapi.ViewConnection{},
			new:     []humioapi.ViewConnection{},
			differ:  false,
		},
		{
			name:    "new connection added",
			current: []humioapi.ViewConnection{},
			new: []humioapi.ViewConnection{
				{
					RepoName: "repo",
					Filter:   "*",
				},
			},
			differ: true,
		},
		{
			name: "update filter",
			current: []humioapi.ViewConnection{
				{
					RepoName: "repo",
					Filter:   "*",
				},
			},
			new: []humioapi.ViewConnection{
				{
					RepoName: "repo",
					Filter:   "* more=",
				},
			},
			differ: true,
		},
		{
			name: "remove connection",
			current: []humioapi.ViewConnection{
				{
					RepoName: "repo",
					Filter:   "*",
				},
			},
			new:    []humioapi.ViewConnection{},
			differ: true,
		},
		{
			name: "reorder connections where name differs",
			current: []humioapi.ViewConnection{
				{
					RepoName: "repo-a",
					Filter:   "*",
				},
				{
					RepoName: "repo-b",
					Filter:   "*",
				},
			},
			new: []humioapi.ViewConnection{
				{
					RepoName: "repo-b",
					Filter:   "*",
				},
				{
					RepoName: "repo-a",
					Filter:   "*",
				},
			},
			differ: false,
		},
		{
			name: "reorder connections where filter differs",
			current: []humioapi.ViewConnection{
				{
					RepoName: "repo",
					Filter:   "a=*",
				},
				{
					RepoName: "repo",
					Filter:   "b=*",
				},
			},
			new: []humioapi.ViewConnection{
				{
					RepoName: "repo",
					Filter:   "b=*",
				},
				{
					RepoName: "repo",
					Filter:   "a=*",
				},
			},
			differ: false,
		},
	}
	for _, tc := range tt {
		t.Run(tc.name, func(t *testing.T) {
			result := viewConnectionsDiffer(tc.current, tc.new)
			if result != tc.differ {
				t.Errorf("viewConnectionsDiffer() got = %v, want %v", result, tc.differ)
			}
		})
	}
}
