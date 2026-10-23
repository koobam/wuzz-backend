package main

import (
	"reflect"
	"testing"
)

func TestDestructiveUp(t *testing.T) {
	cases := []struct {
		name string
		sql  string
		want []string
	}{
		{"adding is fine", "-- +goose Up\nCREATE TABLE t (id int);\nALTER TABLE t ADD COLUMN x int;\nCREATE INDEX i ON t (x);\n\n-- +goose Down\nDROP TABLE t;\n", nil},
		{"the Down half may drop", "-- +goose Up\nSELECT 1;\n-- +goose Down\nDROP TABLE t;\nALTER TABLE t DROP COLUMN x;\n", nil},
		{"drop table", "-- +goose Up\nDROP TABLE old;\n-- +goose Down\nSELECT 1;\n", []string{"drops a table"}},
		{"drop column", "-- +goose Up\nALTER TABLE t DROP COLUMN x;\n-- +goose Down\nSELECT 1;\n", []string{"drops a column"}},
		{"rename column", "-- +goose Up\nALTER TABLE t RENAME COLUMN a TO b;\n-- +goose Down\nSELECT 1;\n", []string{"renames a table or a column"}},
		{"rename table", "-- +goose Up\nALTER TABLE t RENAME TO u;\n-- +goose Down\nSELECT 1;\n", []string{"renames a table or a column"}},
		{"truncate", "-- +goose Up\nTRUNCATE t;\n-- +goose Down\nSELECT 1;\n", []string{"truncates a table"}},
		{"change type", "-- +goose Up\nALTER TABLE t ALTER COLUMN a TYPE bigint;\n-- +goose Down\nSELECT 1;\n", []string{"changes a column's type"}},
		{"a comment that mentions it is not a statement", "-- +goose Up\n-- we never DROP TABLE here\nSELECT 1;\n-- +goose Down\nSELECT 1;\n", nil},
		{"replacing a constraint is allowed", "-- +goose Up\nALTER TABLE t DROP CONSTRAINT c, ADD CONSTRAINT c CHECK (true);\n-- +goose Down\nSELECT 1;\n", nil},
		{"lower case is still caught", "-- +goose Up\nalter table t drop column x;\n-- +goose Down\nSELECT 1;\n", []string{"drops a column"}},
	}
	for _, c := range cases {
		if got := destructiveUp(c.sql); !reflect.DeepEqual(got, c.want) {
			t.Errorf("%s: destructiveUp = %v, want %v", c.name, got, c.want)
		}
	}
}
