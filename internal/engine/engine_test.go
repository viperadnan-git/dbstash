package engine

import (
	"context"
	"net/url"
	"os"
	"slices"
	"strings"
	"testing"

	"github.com/viperadnan-git/dbstash/internal/config"
)

func TestNew(t *testing.T) {
	for _, key := range []string{"pg", "mongo", "mysql", "mariadb", "redis", "PG"} {
		if _, err := New(key); err != nil {
			t.Errorf("New(%q): %v", key, err)
		}
	}
	if _, err := New("oracle"); err == nil {
		t.Error("New(oracle): expected error")
	}
}

func TestURIHelpers(t *testing.T) {
	tests := []struct {
		uri, db, stripped string
	}{
		{"mongodb://u:p@h:27017/app?authSource=admin", "app", "mongodb://u:p@h:27017/?authSource=admin"},
		{"mongodb+srv://u:p@cluster.example.net/app", "app", "mongodb+srv://u:p@cluster.example.net/"},
		{"postgresql://u:p@h:5432/prod", "prod", "postgresql://u:p@h:5432/"},
		{"mongodb://h:27017", "", "mongodb://h:27017/"},
	}
	for _, tt := range tests {
		if got := dbNameFromURI(tt.uri); got != tt.db {
			t.Errorf("dbNameFromURI(%q) = %q, want %q", tt.uri, got, tt.db)
		}
		if got := stripDBFromURI(tt.uri); got != tt.stripped {
			t.Errorf("stripDBFromURI(%q) = %q, want %q", tt.uri, got, tt.stripped)
		}
	}
}

func TestDumpCommand(t *testing.T) {
	tests := []struct {
		name    string
		engine  string
		cfg     config.Config
		mode    string
		out     string
		bin     string
		args    []string
		wantErr string
	}{
		{
			name: "mongo stream gzip", engine: "mongo", mode: "stream",
			cfg: config.Config{DBURI: "mongodb://h/app", BackupCompress: true},
			bin: "mongodump", args: []string{"--archive", "--gzip", "--uri=mongodb://h/app", "--db=app"},
		},
		{
			name: "mongo file all databases strips db", engine: "mongo", mode: "file", out: "/tmp/x.archive",
			cfg: config.Config{DBURI: "mongodb://h/app", BackupAllDatabases: true},
			bin: "mongodump", args: []string{"--archive=/tmp/x.archive", "--uri=mongodb://h/"},
		},
		{
			name: "mongo directory with host and extra args", engine: "mongo", mode: "directory", out: "/tmp/d",
			cfg: config.Config{DBHost: "h", DBPort: "27017", DBName: "app", DBAuthSource: "admin", DumpExtraArgs: "--quiet"},
			bin: "mongodump", args: []string{"--out=/tmp/d", "--host=h", "--port=27017", "--db=app", "--authenticationDatabase=admin", "--quiet"},
		},
		{
			name: "mongo uri without db", engine: "mongo", mode: "stream",
			cfg: config.Config{DBURI: "mongodb://h"}, wantErr: "no database name",
		},
		{
			name: "mongo bad mode", engine: "mongo", mode: "tar",
			cfg: config.Config{DBURI: "mongodb://h/app"}, wantErr: "unsupported mode",
		},
		{
			name: "pg stream custom", engine: "pg", mode: "stream",
			cfg: config.Config{DBURI: "postgresql://h/prod", BackupCompress: true},
			bin: "pg_dump", args: []string{"--format=custom", "postgresql://h/prod"},
		},
		{
			name: "pg file plain", engine: "pg", mode: "file", out: "/tmp/x.sql",
			cfg: config.Config{DBURI: "postgresql://h/prod"},
			bin: "pg_dump", args: []string{"--format=plain", "--file=/tmp/x.sql", "postgresql://h/prod"},
		},
		{
			name: "pg directory", engine: "pg", mode: "directory", out: "/tmp/d",
			cfg: config.Config{DBURI: "postgresql://h/prod"},
			bin: "pg_dump", args: []string{"--format=directory", "--file=/tmp/d", "postgresql://h/prod"},
		},
		{
			name: "pg all databases uses pg_dumpall", engine: "pg", mode: "stream",
			cfg: config.Config{DBURI: "postgresql://h/prod", BackupAllDatabases: true},
			bin: "pg_dumpall", args: []string{"-d", "postgresql://h/"},
		},
		{
			name: "pg all databases rejects directory", engine: "pg", mode: "directory",
			cfg: config.Config{DBURI: "postgresql://h/prod", BackupAllDatabases: true}, wantErr: "only supports stream/file",
		},
		{
			name: "pg uri without db", engine: "pg", mode: "stream",
			cfg: config.Config{DBURI: "postgresql://h"}, wantErr: "no database name",
		},
		{
			name: "mysql stream from uri", engine: "mysql", mode: "stream",
			cfg: config.Config{DBURI: "mysql://u@h:3306/shop"},
			bin: "mysqldump", args: []string{"--host=h", "--port=3306", "--user=u", "shop"},
		},
		{
			name: "mariadb all databases", engine: "mariadb", mode: "file",
			cfg: config.Config{DBHost: "h", DBUser: "root", BackupAllDatabases: true},
			bin: "mariadb-dump", args: []string{"--host=h", "--user=root", "--all-databases"},
		},
		{
			name: "mysql directory uses --tab", engine: "mysql", mode: "directory", out: "/tmp/d",
			cfg: config.Config{DBHost: "h", DBName: "shop"},
			bin: "mysqldump", args: []string{"--host=h", "--tab=/tmp/d", "shop"},
		},
		{
			name: "mysql directory with all databases", engine: "mysql", mode: "directory",
			cfg: config.Config{DBHost: "h", BackupAllDatabases: true}, wantErr: "incompatible with directory mode",
		},
		{
			name: "mysql without database", engine: "mysql", mode: "stream",
			cfg: config.Config{DBHost: "h"}, wantErr: "no database name",
		},
		{
			name: "redis rejects stream", engine: "redis", mode: "stream",
			cfg: config.Config{DBURI: "redis://h:6379"}, wantErr: "only supports file mode",
		},
		{
			name: "redis file mode writes the path itself", engine: "redis", mode: "file", out: "/tmp/x.rdb",
			cfg: config.Config{DBURI: "redis://h:6379"},
			bin: "redis-cli", args: []string{"-h", "h", "-p", "6379", "--rdb", "/tmp/x.rdb"},
		},
		{
			name: "redis rejects directory", engine: "redis", mode: "directory",
			cfg: config.Config{DBURI: "redis://h"}, wantErr: "only supports file mode",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			eng, err := New(tt.engine)
			if err != nil {
				t.Fatal(err)
			}
			cmd, cleanup, err := eng.DumpCommand(context.Background(), &tt.cfg, tt.mode, tt.out)
			if tt.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("err = %v, want containing %q", err, tt.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			cleanup()
			if cmd.Args[0] != tt.bin || !slices.Equal(cmd.Args[1:], tt.args) {
				t.Errorf("got %q, want %s %q", cmd.Args, tt.bin, tt.args)
			}
			if cmd.Cancel == nil || cmd.WaitDelay == 0 {
				t.Error("dump command must stop gracefully on cancellation (proc.Command)")
			}
		})
	}
}

// Credentials must never appear in argv, which any local user can read via ps.
func TestDumpCommand_CredentialsOutOfArgs(t *testing.T) {
	const pw = `p@ss w#rd"'\x`
	enc := url.UserPassword("u", pw).String()
	tests := []struct {
		engine string
		cfg    config.Config
		mode   string
	}{
		{"pg", config.Config{DBURI: "postgresql://" + enc + "@h/db"}, "stream"},
		{"pg", config.Config{DBURI: "postgresql://" + enc + "@h/db", BackupAllDatabases: true}, "stream"},
		{"pg", config.Config{DBHost: "h", DBName: "db", DBUser: "u", DBPassword: pw}, "stream"},
		{"mongo", config.Config{DBURI: "mongodb://" + enc + "@h/db"}, "stream"},
		{"mongo", config.Config{DBHost: "h", DBName: "db", DBUser: "u", DBPassword: pw}, "stream"},
		{"mysql", config.Config{DBURI: "mysql://" + enc + "@h/db"}, "stream"},
		{"mariadb", config.Config{DBHost: "h", DBName: "db", DBUser: "u", DBPassword: pw}, "file"},
		{"redis", config.Config{DBURI: "redis://" + url.UserPassword("", pw).String() + "@h"}, "file"},
	}
	for _, tt := range tests {
		t.Run(tt.engine, func(t *testing.T) {
			eng, _ := New(tt.engine)
			cmd, cleanup, err := eng.DumpCommand(context.Background(), &tt.cfg, tt.mode, "/tmp/out")
			if err != nil {
				t.Fatal(err)
			}
			args := strings.Join(cmd.Args, " ")
			if strings.Contains(args, "p@ss") || strings.Contains(args, "p%40ss") {
				t.Fatalf("password in args: %q", cmd.Args)
			}
			var file string
			for _, a := range cmd.Args {
				if _, f, ok := strings.Cut(a, "--defaults-extra-file="); ok {
					file = f
				}
				if _, f, ok := strings.Cut(a, "--config="); ok {
					file = f
				}
			}
			switch {
			case file != "":
				if tt.engine != "mongo" && cmd.Args[1] != "--defaults-extra-file="+file {
					t.Errorf("--defaults-extra-file must be the first argument: %q", cmd.Args)
				}
				if fi, err := os.Stat(file); err != nil || fi.Mode().Perm() != 0o600 {
					t.Fatalf("option file %s: %v %v", file, fi, err)
				}
				cleanup()
				if _, err := os.Stat(file); !os.IsNotExist(err) {
					t.Errorf("cleanup left %s", file)
				}
			case slices.ContainsFunc(cmd.Env, func(e string) bool { return strings.HasSuffix(e, "="+pw) }):
				cleanup()
			default:
				t.Fatalf("password passed nowhere: args=%q", cmd.Args)
			}
		})
	}
}

func TestOptionValue(t *testing.T) {
	for in, want := range map[string]string{
		"pw":      `"pw"`,
		`a#b c`:   `"a#b c"`,
		`a"b`:     `'a"b'`,
		"a\\b\nc": `"a\\b\nc"`,
	} {
		if got := optionValue(in); got != want {
			t.Errorf("optionValue(%q) = %s, want %s", in, got, want)
		}
	}
}
