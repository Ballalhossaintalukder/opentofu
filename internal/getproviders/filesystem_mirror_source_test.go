// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package getproviders

import (
	"context"
	"strings"
	"testing"

	"github.com/apparentlymart/go-versions/versions"
	"github.com/google/go-cmp/cmp"
	"github.com/opentofu/svchost"

	"github.com/opentofu/opentofu/internal/addrs"
)

func TestFilesystemMirrorSourceAllAvailablePackages(t *testing.T) {
	source := NewFilesystemMirrorSource(t.Context(), "testdata/filesystem-mirror")
	got, err := source.AllAvailablePackages()
	if err != nil {
		t.Fatal(err)
	}

	want := map[addrs.Provider]PackageMetaList{
		nullProvider: {
			{
				Provider:       nullProvider,
				Version:        versions.MustParseVersion("2.0.0"),
				TargetPlatform: Platform{"darwin", "amd64"},
				Filename:       "terraform-provider-null_2.0.0_darwin_amd64.zip",
				Location:       PackageLocalDir("testdata/filesystem-mirror/registry.opentofu.org/hashicorp/null/2.0.0/darwin_amd64"),
			},
			{
				Provider:       nullProvider,
				Version:        versions.MustParseVersion("2.0.0"),
				TargetPlatform: Platform{"linux", "amd64"},
				Filename:       "terraform-provider-null_2.0.0_linux_amd64.zip",
				Location:       PackageLocalDir("testdata/filesystem-mirror/registry.opentofu.org/hashicorp/null/2.0.0/linux_amd64"),
			},
			{
				Provider:       nullProvider,
				Version:        versions.MustParseVersion("2.1.0"),
				TargetPlatform: Platform{"linux", "amd64"},
				Filename:       "terraform-provider-null_2.1.0_linux_amd64.zip",
				Location:       PackageLocalArchive("testdata/filesystem-mirror/registry.opentofu.org/hashicorp/null/terraform-provider-null_2.1.0_linux_amd64.zip"),
			},
			{
				Provider:       nullProvider,
				Version:        versions.MustParseVersion("2.0.0"),
				TargetPlatform: Platform{"windows", "amd64"},
				Filename:       "terraform-provider-null_2.0.0_windows_amd64.zip",
				Location:       PackageLocalDir("testdata/filesystem-mirror/registry.opentofu.org/hashicorp/null/2.0.0/windows_amd64"),
			},
		},
		randomBetaProvider: {
			{
				Provider:       randomBetaProvider,
				Version:        versions.MustParseVersion("1.2.0"),
				TargetPlatform: Platform{"linux", "amd64"},
				Filename:       "terraform-provider-random-beta_1.2.0_linux_amd64.zip",
				Location:       PackageLocalDir("testdata/filesystem-mirror/registry.opentofu.org/hashicorp/random-beta/1.2.0/linux_amd64"),
			},
		},
		randomProvider: {
			{
				Provider:       randomProvider,
				Version:        versions.MustParseVersion("1.2.0"),
				TargetPlatform: Platform{"linux", "amd64"},
				Filename:       "terraform-provider-random_1.2.0_linux_amd64.zip",
				Location:       PackageLocalDir("testdata/filesystem-mirror/registry.opentofu.org/hashicorp/random/1.2.0/linux_amd64"),
			},
		},

		happycloudProvider: {
			{
				Provider:       happycloudProvider,
				Version:        versions.MustParseVersion("0.1.0-alpha.2"),
				TargetPlatform: Platform{"darwin", "amd64"},
				Filename:       "terraform-provider-happycloud_0.1.0-alpha.2_darwin_amd64.zip",
				Location:       PackageLocalDir("testdata/filesystem-mirror/tfe.example.com/AwesomeCorp/happycloud/0.1.0-alpha.2/darwin_amd64"),
			},
		},
		legacyProvider: {
			{
				Provider:       legacyProvider,
				Version:        versions.MustParseVersion("1.0.0"),
				TargetPlatform: Platform{"linux", "amd64"},
				Filename:       "terraform-provider-legacy_1.0.0_linux_amd64.zip",
				Location:       PackageLocalDir("testdata/filesystem-mirror/registry.opentofu.org/-/legacy/1.0.0/linux_amd64"),
			},
		},
	}

	if diff := cmp.Diff(want, got); diff != "" {
		t.Errorf("incorrect result\n%s", diff)
	}
}

// In this test the directory layout is invalid (missing the hostname
// subdirectory). The provider installer should ignore the invalid directory.
func TestFilesystemMirrorSourceAllAvailablePackages_invalid(t *testing.T) {
	source := NewFilesystemMirrorSource(t.Context(), "testdata/filesystem-mirror-invalid")
	_, err := source.AllAvailablePackages()
	if err != nil {
		t.Fatal(err)
	}
}

func TestFilesystemMirrorSourceAvailableVersions(t *testing.T) {
	source := NewFilesystemMirrorSource(t.Context(), "testdata/filesystem-mirror")
	got, _, err := source.AvailableVersions(context.Background(), nullProvider)
	if err != nil {
		t.Fatal(err)
	}

	want := VersionList{
		versions.MustParseVersion("2.0.0"),
		versions.MustParseVersion("2.1.0"),
	}

	if diff := cmp.Diff(want, got); diff != "" {
		t.Errorf("incorrect result\n%s", diff)
	}
}

func TestFilesystemMirrorSourceAvailableVersions_Unspecified(t *testing.T) {
	unspecifiedProvider := addrs.Provider{
		Hostname:  svchost.Hostname("registry.opentofu.org"),
		Namespace: "testnamespace",
		Type:      "unspecified",
	}
	source := NewFilesystemMirrorSource(t.Context(), "testdata/filesystem-mirror-unspecified")
	got, warn, err := source.AvailableVersions(context.Background(), unspecifiedProvider)
	if err != nil {
		t.Fatal(err)
	}
	// Check that we got the unspecified version
	if len(got) != 1 || got[0] != versions.Unspecified {
		t.Fatalf("expected unspecified version, got %v", got)
	}
	// We should have unspecified (0.0.0) version warning
	if len(warn) != 1 {
		t.Fatalf("expected 1 warning, got %v", warn)
	}
	warningBit := "unspecified (0.0.0) version available in the filesystem mirror"
	if !strings.Contains(warn[0], warningBit) {
		t.Fatalf("expected warning to contain %q, got %q", warningBit, warn[0])
	}
}
func TestFilesystemMirrorSourcePackageMeta(t *testing.T) {
	t.Run("available platform", func(t *testing.T) {
		source := NewFilesystemMirrorSource(t.Context(), "testdata/filesystem-mirror")
		got, err := source.PackageMeta(
			context.Background(),
			nullProvider,
			versions.MustParseVersion("2.0.0"),
			Platform{"linux", "amd64"},
		)
		if err != nil {
			t.Fatal(err)
		}

		want := PackageMeta{
			Provider:       nullProvider,
			Version:        versions.MustParseVersion("2.0.0"),
			TargetPlatform: Platform{"linux", "amd64"},
			Filename:       "terraform-provider-null_2.0.0_linux_amd64.zip",
			Location:       PackageLocalDir("testdata/filesystem-mirror/registry.opentofu.org/hashicorp/null/2.0.0/linux_amd64"),
		}

		if diff := cmp.Diff(want, got); diff != "" {
			t.Errorf("incorrect result\n%s", diff)
		}
	})
	t.Run("unavailable platform", func(t *testing.T) {
		source := NewFilesystemMirrorSource(t.Context(), "testdata/filesystem-mirror")
		// We'll request a version that does exist in the fixture directory,
		// but for a platform that isn't supported.
		_, err := source.PackageMeta(
			context.Background(),
			nullProvider,
			versions.MustParseVersion("2.0.0"),
			Platform{"nonexist", "nonexist"},
		)

		if err == nil {
			t.Fatalf("succeeded; want error")
		}

		// This specific error type is important so callers can use it to
		// generate an actionable error message e.g. by checking to see if
		// _any_ versions of this provider support the given platform, or
		// similar helpful hints.
		wantErr := ErrPlatformNotSupported{
			Provider: nullProvider,
			Version:  versions.MustParseVersion("2.0.0"),
			Platform: Platform{"nonexist", "nonexist"},
		}
		if diff := cmp.Diff(wantErr, err); diff != "" {
			t.Errorf("incorrect error\n%s", diff)
		}
	})
}

var nullProvider = addrs.Provider{
	Hostname:  svchost.Hostname("registry.opentofu.org"),
	Namespace: "hashicorp",
	Type:      "null",
}
var randomProvider = addrs.Provider{
	Hostname:  svchost.Hostname("registry.opentofu.org"),
	Namespace: "hashicorp",
	Type:      "random",
}
var randomBetaProvider = addrs.Provider{
	Hostname:  svchost.Hostname("registry.opentofu.org"),
	Namespace: "hashicorp",
	Type:      "random-beta",
}
var happycloudProvider = addrs.Provider{
	Hostname:  svchost.Hostname("tfe.example.com"),
	Namespace: "awesomecorp",
	Type:      "happycloud",
}
var legacyProvider = addrs.NewLegacyProvider("legacy")
