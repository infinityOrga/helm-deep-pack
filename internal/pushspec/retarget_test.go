package pushspec

import "testing"

func TestRetarget(t *testing.T) {
	tests := []struct {
		name, target, path, want string
		wantErr                  bool
	}{
		{name: "tag", target: "library/redis:7", path: "team/redis", want: "team/redis:7"},
		{name: "digest", target: "library/redis@sha256:abc", path: "team/redis", want: "team/redis@sha256:abc"},
		{name: "trimmed", target: "redis:7", path: "  team/redis \n", want: "team/redis:7"},
		{name: "same", target: "redis:7", path: "redis", want: "redis:7"},
		{name: "empty", target: "redis:7", path: "  ", wantErr: true},
		{name: "tag in path", target: "redis:7", path: "team/redis:8", wantErr: true},
		{name: "digest in path", target: "redis:7", path: "team/redis@sha256:abc", wantErr: true},
		{name: "host", target: "redis:7", path: "quay.io/team/redis", wantErr: true},
		{name: "localhost", target: "redis:7", path: "localhost/redis", wantErr: true},
		{name: "invalid chars", target: "redis:7", path: "Team/Re dis", wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := Retarget(ArchiveSpec{Image: "x", Target: tt.target}, tt.path)
			if (err != nil) != tt.wantErr {
				t.Fatalf("err = %v, wantErr %v", err, tt.wantErr)
			}
			if err == nil && (got.Target != tt.want || got.Image != "x") {
				t.Fatalf("got %+v, want target %q", got, tt.want)
			}
		})
	}
}

func TestDuplicateTarget(t *testing.T) {
	specs := []ArchiveSpec{{Image: "a", Target: "x:1"}, {Image: "b", Target: "y:1"}, {Image: "c", Target: "x:1"}}
	a, b, found := DuplicateTarget(specs)
	if !found || a.Image != "a" || b.Image != "c" {
		t.Fatalf("got %v %v %v", a, b, found)
	}
	if _, _, found := DuplicateTarget(specs[:2]); found {
		t.Fatal("unexpected duplicate")
	}
}
