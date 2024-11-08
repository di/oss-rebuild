package apiservice

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/go-git/go-billy/v5/memfs"
	"github.com/go-git/go-git/v5/plumbing"
	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"
	"github.com/google/oss-rebuild/internal/gcb/gcbtest"
	"github.com/google/oss-rebuild/internal/httpx/httpxtest"
	"github.com/google/oss-rebuild/pkg/archive"
	"github.com/google/oss-rebuild/pkg/archive/archivetest"
	"github.com/google/oss-rebuild/pkg/rebuild/cratesio"
	"github.com/google/oss-rebuild/pkg/rebuild/debian"
	"github.com/google/oss-rebuild/pkg/rebuild/npm"
	"github.com/google/oss-rebuild/pkg/rebuild/pypi"
	"github.com/google/oss-rebuild/pkg/rebuild/rebuild"
	"github.com/google/oss-rebuild/pkg/rebuild/schema"
	"github.com/secure-systems-lab/go-securesystemslib/dsse"
	"google.golang.org/api/cloudbuild/v1"
)

type FakeSigner struct{}

func (FakeSigner) Sign(ctx context.Context, data []byte) ([]byte, error) {
	return []byte("just trust me"), nil
}
func (FakeSigner) KeyID() (string, error) {
	return "fake", nil
}

func TestRebuildPackage(t *testing.T) {
	for _, tc := range []struct {
		target      rebuild.Target
		calls       []httpxtest.Call
		strategy    rebuild.Strategy
		file        *bytes.Buffer
		expectedMsg string
	}{
		{
			target: rebuild.Target{Ecosystem: rebuild.PyPI, Package: "absl-py", Version: "2.0.0", Artifact: "absl_py-2.0.0-py3-none-any.whl"},
			calls: []httpxtest.Call{
				{
					URL: "https://pypi.org/pypi/absl-py/2.0.0/json",
					Response: &http.Response{
						StatusCode: 200,
						Body: io.NopCloser(bytes.NewReader([]byte(`{
              "info": {
                  "name": "absl-py",
                  "version": "2.0.0"
              },
              "urls": [
                  {
                      "filename": "absl_py-2.0.0-py3-none-any.whl",
                      "url": "https://files.pythonhosted.org/packages/01/e4/abcd.../absl_py-2.0.0-py3-none-any.whl"
                  }
              ]
          }`))),
					},
				},
				{
					URL: "https://files.pythonhosted.org/packages/01/e4/abcd.../absl_py-2.0.0-py3-none-any.whl",
					Response: &http.Response{
						StatusCode: 200,
						Body: io.NopCloser(must(archivetest.ZipFile([]archive.ZipEntry{
							{FileHeader: &zip.FileHeader{Name: "foo", Modified: time.UnixMilli(0)}, Body: []byte("foo")},
						}))),
					},
				},
			},
			strategy: &pypi.PureWheelBuild{
				Location: rebuild.Location{Repo: "foo", Ref: "aaaabbbbccccddddeeeeaaaabbbbccccddddeeee", Dir: "foo"},
			},
			file: must(archivetest.ZipFile([]archive.ZipEntry{
				{FileHeader: &zip.FileHeader{Name: "foo", Modified: time.UnixMilli(0)}, Body: []byte("foo")},
			})),
		},
		{
			target: rebuild.Target{Ecosystem: rebuild.PyPI, Package: "absl-py", Version: "2.0.0", Artifact: "absl_py-2.0.0-py3-none-any.whl"},
			calls: []httpxtest.Call{
				{
					URL: "https://pypi.org/pypi/absl-py/2.0.0/json",
					Response: &http.Response{
						StatusCode: 200,
						Body: io.NopCloser(bytes.NewReader([]byte(`{
              "info": {
                  "name": "absl-py",
                  "version": "2.0.0"
              },
              "urls": [
                  {
                      "filename": "absl_py-2.0.0-py3-none-any.whl",
                      "url": "https://files.pythonhosted.org/packages/01/e4/abcd.../absl_py-2.0.0-py3-none-any.whl"
                  }
              ]
          }`))),
					},
				},
				{
					URL: "https://files.pythonhosted.org/packages/01/e4/abcd.../absl_py-2.0.0-py3-none-any.whl",
					Response: &http.Response{
						StatusCode: 200,
						Body: io.NopCloser(must(archivetest.ZipFile([]archive.ZipEntry{
							{FileHeader: &zip.FileHeader{Name: "totally-not-foo"}, Body: []byte("not-in-the-least-foo")},
						}))),
					},
				},
			},
			strategy: &pypi.PureWheelBuild{
				Location: rebuild.Location{Repo: "foo", Ref: "aaaabbbbccccddddeeeeaaaabbbbccccddddeeee", Dir: "foo"},
			},
			file: must(archivetest.ZipFile([]archive.ZipEntry{
				{FileHeader: &zip.FileHeader{Name: "foo", Modified: time.UnixMilli(0)}, Body: []byte("foo")},
			})),
			expectedMsg: "rebuild content mismatch",
		},
		{
			target: rebuild.Target{Ecosystem: rebuild.CratesIO, Package: "serde", Version: "1.0.150", Artifact: "serde-1.0.150.crate"},
			calls: []httpxtest.Call{
				{
					URL: "https://crates.io/api/v1/crates/serde/1.0.150",
					Response: &http.Response{
						StatusCode: 200,
						Body:       io.NopCloser(bytes.NewReader([]byte(`{"version":{"num":"1.0.150", "dl_path":"/api/v1/crates/serde/1.0.150/download"}}`))),
					},
				},
				{
					URL: "https://crates.io/api/v1/crates/serde/1.0.150/download",
					Response: &http.Response{
						StatusCode: 200,
						Body: io.NopCloser(must(archivetest.TgzFile([]archive.TarEntry{
							{Header: &tar.Header{Name: "foo"}, Body: []byte("foo")},
						}))),
					},
				},
			},
			strategy: &cratesio.CratesIOCargoPackage{
				Location:    rebuild.Location{Repo: "foo", Ref: "aaaabbbbccccddddeeeeaaaabbbbccccddddeeee", Dir: "foo"},
				RustVersion: "1.65.0",
			},
			file: must(archivetest.TgzFile([]archive.TarEntry{
				{Header: &tar.Header{Name: "foo"}, Body: []byte("foo")},
			})),
		},
		{
			target: rebuild.Target{Ecosystem: rebuild.NPM, Package: "express", Version: "4.18.2", Artifact: "express-4.18.2.tgz"},
			calls: []httpxtest.Call{
				{
					URL: "https://registry.npmjs.org/express/4.18.2",
					Response: &http.Response{
						StatusCode: 200,
						Body:       io.NopCloser(bytes.NewReader([]byte(`{"name":"express","dist-tags":{"latest":"4.18.2"},"dist":{"tarball":"https://registry.npmjs.org/express/-/express-4.18.2.tgz"}}`))),
					},
				},
				{
					URL: "https://registry.npmjs.org/express/-/express-4.18.2.tgz",
					Response: &http.Response{
						StatusCode: 200,
						Body: io.NopCloser(must(archivetest.TgzFile([]archive.TarEntry{
							{Header: &tar.Header{Name: "foo"}, Body: []byte("foo")},
						}))),
					},
				},
			},
			strategy: &npm.NPMPackBuild{
				Location:   rebuild.Location{Repo: "foo", Ref: "aaaabbbbccccddddeeeeaaaabbbbccccddddeeee", Dir: "foo"},
				NPMVersion: "8.12.1",
			},
			file: must(archivetest.TgzFile([]archive.TarEntry{
				{Header: &tar.Header{Name: "foo"}, Body: []byte("foo")},
			})),
		},
		{
			target: rebuild.Target{Ecosystem: rebuild.Debian, Package: "main/xz-utils", Version: "5.2.4-1+b1", Artifact: "xz-utils_5.2.4-1+b1_amd64.deb"},
			calls: []httpxtest.Call{
				{
					URL: "https://deb.debian.org/debian/pool/main/x/xz-utils/xz-utils_5.2.4-1+b1_amd64.deb",
					Response: &http.Response{
						StatusCode: 200,
						Body:       io.NopCloser(bytes.NewReader([]byte("deb_contents"))),
					},
				},
				{
					URL: "https://deb.debian.org/debian/pool/main/x/xz-utils/xz-utils_5.2.4-1.dsc",
					Response: &http.Response{
						StatusCode: 200,
						Body: io.NopCloser(bytes.NewReader([]byte(`-----BEGIN PGP SIGNED MESSAGE-----
Hash: SHA256

Format: 3.0 (quilt)
Source: xz-utils
Binary: bin-a, bin-b, xz-utils
Build-Depends: debhelper (>= 8.9.0), autopoint | gettext (<< 0.18-1)
Build-Depends-Indep: doxygen
Package-List:
 liblzma-dev deb libdevel optional arch=any
 liblzma-doc deb doc optional arch=all
Files:
 003e4d0b1b1899fc6e3000b24feddf7c 1053868 xz-utils_5.2.4.orig.tar.xz
 e475651d39fac8c38ff1460c1d92fc2e 879 xz-utils_5.2.4.orig.tar.xz.asc
 5d018428dac6a83f00c010f49c51836e 135296 xz-utils_5.2.4-1.debian.tar.xz

-----BEGIN PGP SIGNATURE-----

iQJHBAEBCAAxFiEEUh5Y8X6W1xKqD/EC38Zx7rMz+iUFAlxOW5QTHGpybmllZGVy
RLpmHHG1JOVdOA==
=WDR2
-----END PGP SIGNATURE-----`,
						))),
					},
				},
			},
			strategy: &debian.DebianPackage{
				DSC: debian.FileWithChecksum{
					URL: "https://deb.debian.org/debian/pool/main/x/xz-utils/xz-utils_5.2.4-1.dsc",
					MD5: "",
				},
				Orig: debian.FileWithChecksum{
					URL: "https://deb.debian.org/debian/pool/main/x/xz-utils/xz-utils_5.2.4.orig.tar.xz",
					MD5: "003e4d0b1b1899fc6e3000b24feddf7c",
				},
				Debian: debian.FileWithChecksum{
					URL: "https://deb.debian.org/debian/pool/main/x/xz-utils/xz-utils_5.2.4-1.debian.tar.xz",
					MD5: "5d018428dac6a83f00c010f49c51836e",
				},
				Requirements: []string{"debhelper", "autopoint", "doxygen"},
			},
			file: bytes.NewBuffer([]byte("deb_contents")),
		},
	} {
		t.Run(string(tc.target.Ecosystem), func(t *testing.T) {
			ctx := context.Background()
			var d RebuildPackageDeps
			d.HTTPClient = &httpxtest.MockClient{
				Calls: tc.calls,
				URLValidator: func(expected, actual string) {
					if diff := cmp.Diff(expected, actual); diff != "" {
						t.Errorf("URL mismatch: diff\n%v", diff)
					}
				},
			}
			d.Signer = must(dsse.NewEnvelopeSigner(&FakeSigner{}))
			fs := memfs.New()
			afs := must(fs.Chroot("attestations"))
			d.AttestationStore = rebuild.NewFilesystemAssetStore(afs)
			remoteMetadata := rebuild.NewFilesystemAssetStore(must(fs.Chroot("remote-metadata")))
			d.RemoteMetadataStoreBuilder = func(ctx context.Context, id string) (rebuild.LocatableAssetStore, error) {
				return remoteMetadata, nil
			}
			d.LocalMetadataStore = rebuild.NewFilesystemAssetStore(must(fs.Chroot("local-metadata")))
			buildSteps := []*cloudbuild.BuildStep{
				{Name: "gcr.io/foo/bar", Script: "./bar"},
			}
			d.GCBClient = &gcbtest.MockClient{
				CreateBuildFunc: func(ctx context.Context, project string, build *cloudbuild.Build) (*cloudbuild.Operation, error) {
					c := must(remoteMetadata.Writer(ctx, rebuild.Asset{Type: rebuild.RebuildAsset, Target: tc.target}))
					defer func() { must1(c.Close()) }()
					must(c.Write(tc.file.Bytes()))
					return &cloudbuild.Operation{
						Name: "operations/build-id",
						Done: false,
						Metadata: must(json.Marshal(cloudbuild.BuildOperationMetadata{Build: &cloudbuild.Build{
							Id:     "build-id",
							Status: "QUEUED",
							Steps:  buildSteps,
						}})),
					}, nil
				},
				WaitForOperationFunc: func(ctx context.Context, op *cloudbuild.Operation) (*cloudbuild.Operation, error) {
					return &cloudbuild.Operation{
						Name: "operations/build-id",
						Done: true,
						Metadata: must(json.Marshal(cloudbuild.BuildOperationMetadata{Build: &cloudbuild.Build{
							Id:         "build-id",
							Status:     "SUCCESS",
							FinishTime: "2024-05-08T15:23:00Z",
							Steps:      buildSteps,
							Results:    &cloudbuild.Results{BuildStepImages: []string{"sha256:abcd"}},
						}})),
					}, nil
				},
			}
			d.BuildProject = "foo-project"
			d.BuildServiceAccount = "foo-role"
			d.UtilPrebuildBucket = "foo-prebuild-bucket"
			d.BuildLogsBucket = "foo-logs-bucket"
			d.BuildDefRepo = rebuild.Location{
				Repo: "https://github.internal/foo/build-def-repo",
				Ref:  plumbing.Main.String(),
				Dir:  ".",
			}
			d.OverwriteAttestations = false
			d.InferStub = func(context.Context, schema.InferenceRequest) (*schema.StrategyOneOf, error) {
				oneof := schema.NewStrategyOneOf(tc.strategy)
				must(oneof.Strategy())
				return &oneof, nil
			}

			verdict, err := rebuildPackage(ctx, schema.RebuildPackageRequest{Ecosystem: tc.target.Ecosystem, Package: tc.target.Package, Version: tc.target.Version}, &d)
			if err != nil {
				t.Fatalf("RebuildPackage(): %v", err)
			}
			if tc.expectedMsg != "" {
				if !strings.Contains(verdict.Message, tc.expectedMsg) {
					t.Fatalf("RebuildPackage(): verdict=%v,want=%s", verdict.Message, tc.expectedMsg)
				}
				return
			}
			if verdict.Message != "" {
				t.Fatalf("RebuildPackage() verdict: %v", verdict.Message)
			}

			dockerfile := must(d.LocalMetadataStore.Reader(ctx, rebuild.Asset{Type: rebuild.DockerfileAsset, Target: tc.target}))
			if len(must(io.ReadAll(dockerfile))) == 0 {
				t.Error("Dockerfile empty")
			}
			buildinfo := must(d.LocalMetadataStore.Reader(ctx, rebuild.Asset{Type: rebuild.BuildInfoAsset, Target: tc.target}))
			diff := cmp.Diff(
				rebuild.BuildInfo{
					Target:      tc.target,
					BuildID:     "build-id",
					BuildImages: map[string]string{"gcr.io/foo/bar": "sha256:abcd"},
					Steps:       buildSteps,
				},
				mustJSON[rebuild.BuildInfo](buildinfo),
				cmpopts.IgnoreFields(rebuild.BuildInfo{}, "ID", "Builder", "BuildStart", "BuildEnd"),
			)
			if diff != "" {
				t.Errorf("BuildInfo diff: %s", diff)
			}
			bundle := must(d.AttestationStore.Reader(ctx, rebuild.Asset{Type: rebuild.AttestationBundleAsset, Target: tc.target}))
			attestations := mustJSONL[map[string]any](bundle)
			if len(attestations) != 2 {
				t.Errorf("Attestation bundle length: want=2 got=%d", len(attestations))
			}
		})
	}
}

func mustJSON[T any](r io.Reader) T {
	var t T
	must1(json.NewDecoder(r).Decode(&t))
	return t
}

func mustJSONL[T any](r io.Reader) []T {
	var ts []T
	d := json.NewDecoder(r)
	for d.More() {
		var t T
		must1(d.Decode(&t))
		ts = append(ts, t)
	}
	return ts
}

func must1(err error) {
	if err != nil {
		panic(err)
	}
}

func must[T any](t T, err error) T {
	must1(err)
	return t
}
