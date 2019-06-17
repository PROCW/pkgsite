// Copyright 2019 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package postgres

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"
	"golang.org/x/discovery/internal"
)

func TestVersionState(t *testing.T) {
	defer ResetTestDB(testDB, t)
	ctx, cancel := context.WithTimeout(context.Background(), testTimeout)
	defer cancel()

	// verify that latest index timestamp works
	initialTime, err := testDB.LatestIndexTimestamp(ctx)
	if err != nil {
		t.Fatalf("testDB.LatestIndexTimestamp(ctx): %v", err)
	}
	if want := (time.Time{}); initialTime != want {
		t.Errorf("testDB.LatestIndexTimestamp(ctx) = %v, want %v", initialTime, want)
	}

	now := NowTruncated()
	latest := now.Add(10 * time.Second)
	// insert a FooVersion with no Timestamp, to ensure that it is later updated
	// on conflict.
	initialFooVersion := &internal.IndexVersion{
		Path:    "foo.com/bar",
		Version: "v1.0.0",
	}
	if err := testDB.InsertIndexVersions(ctx, []*internal.IndexVersion{initialFooVersion}); err != nil {
		t.Fatalf("testDB.InsertIndexVersions(ctx, [%v]): %v", initialFooVersion, err)
	}
	fooVersion := &internal.IndexVersion{
		Path:      "foo.com/bar",
		Version:   "v1.0.0",
		Timestamp: now,
	}
	bazVersion := &internal.IndexVersion{
		Path:      "baz.com/quux",
		Version:   "v2.0.1",
		Timestamp: latest,
	}
	versions := []*internal.IndexVersion{fooVersion, bazVersion}
	if err := testDB.InsertIndexVersions(ctx, versions); err != nil {
		t.Fatalf("testDB.InsertIndexVersions(ctx, %v): %v", versions, err)
	}

	gotVersions, err := testDB.GetNextVersionsToFetch(ctx, 10)
	t.Logf("%+v", gotVersions)
	if err != nil {
		t.Fatalf("testDB.GetVersionsToFetch(ctx, 10): %v", err)
	}

	wantVersions := []*internal.VersionState{
		{ModulePath: "baz.com/quux", Version: "v2.0.1", IndexTimestamp: bazVersion.Timestamp},
		{ModulePath: "foo.com/bar", Version: "v1.0.0", IndexTimestamp: fooVersion.Timestamp},
	}
	ignore := cmpopts.IgnoreFields(internal.VersionState{}, "CreatedAt", "LastProcessedAt", "NextProcessedAfter")
	if diff := cmp.Diff(wantVersions, gotVersions, ignore); diff != "" {
		t.Fatalf("testDB.GetVersionsToFetch(ctx, 10) mismatch (-want +got):\n%s", diff)
	}

	var (
		statusCode = 500
		fetchErr   = errors.New("bad request")
	)
	if err := testDB.UpsertVersionState(ctx, fooVersion.Path, fooVersion.Version, fooVersion.Timestamp, statusCode, fetchErr); err != nil {
		t.Fatalf("testDB.UpsertVersionState(ctx, %q, %q, %d, %v): %v", fooVersion.Path,
			versions[0].Version, statusCode, fetchErr, err)
	}
	errString := fetchErr.Error()
	wantFooState := &internal.VersionState{
		ModulePath:         "foo.com/bar",
		Version:            "v1.0.0",
		IndexTimestamp:     now,
		TryCount:           1,
		Error:              &errString,
		Status:             &statusCode,
		NextProcessedAfter: gotVersions[1].CreatedAt.Add(1 * time.Minute),
	}
	gotFooState, err := testDB.GetVersionState(ctx, wantFooState.ModulePath, wantFooState.Version)
	if err != nil {
		t.Fatalf("testDB.GetVersionState(ctx, %q, %q): %v", wantFooState.ModulePath, wantFooState.Version, err)
	}
	if diff := cmp.Diff(wantFooState, gotFooState, ignore); diff != "" {
		t.Errorf("testDB.GetVersionState(ctx, %q, %q) mismatch (-want +got)\n%s", wantFooState.ModulePath, wantFooState.Version, diff)
	}

	stats, err := testDB.GetVersionStats(ctx)
	if err != nil {
		t.Fatalf("testDB.GetVersionStats(ctx): %v", err)
	}
	wantStats := &VersionStats{
		LatestTimestamp: latest,
		VersionCounts: map[int]int{
			0:   1,
			500: 1,
		},
	}
	if diff := cmp.Diff(wantStats, stats); diff != "" {
		t.Errorf("testDB.GetVersionStats(ctx) mismatch (-want +got):\n%s", diff)
	}
}