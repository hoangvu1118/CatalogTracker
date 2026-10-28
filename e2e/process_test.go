package e2e

import (
	"fmt"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
	"testing"
	"time"

	"github.com/hoangvu1118/catalogtracker/internal/supplier/fake"
	"github.com/hoangvu1118/catalogtracker/internal/testdb"
)

// The real programs, built and run as separate processes. The application
// is killed with SIGKILL (no chance to save anything) in the middle of an
// update, several times. Each restart must show the update as interrupted
// and resume from the bookmark.
func TestKillMinus9RealProcess(t *testing.T) {
	dbURL := testdb.URL(t)
	bin := t.TempDir()
	build := exec.Command("go", "build", "-o", bin+"/", "../cmd/catalog", "../cmd/fakesupplier")
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build: %v\n%s", err, out)
	}

	supAddr, appAddr := freeAddr(t), freeAddr(t)
	sup := exec.Command(filepath.Join(bin, "fakesupplier"),
		"-addr", supAddr, "-generate", "-seed", "99", "-batches", "50", "-delay", "15ms")
	startProcess(t, sup)
	t.Cleanup(func() { sup.Process.Kill(); sup.Wait() })
	waitHTTP(t, "http://"+supAddr+"/feed/latest")

	runApp := func() *exec.Cmd {
		cmd := exec.Command(filepath.Join(bin, "catalog"), "-addr", appAddr,
			"-database-url", dbURL, "-supplier-url", "http://"+supAddr, "-page-size", "2")
		startProcess(t, cmd)
		waitHTTP(t, "http://"+appAddr+"/healthz")
		return cmd
	}

	e := &env{t: t, base: "http://" + appAddr}
	e.feed = fake.Generate(fake.GenerateOptions{Seed: 99, Batches: 50})

	app := runApp()
	id := e.start(false).ID
	kills := 0
	for e.get(id).Status != "completed" {
		time.Sleep(150 * time.Millisecond)
		if e.get(id).Status == "completed" {
			break
		}
		app.Process.Signal(syscall.SIGKILL)
		app.Wait()
		kills++

		app = runApp()
		u := e.get(id)
		if u.Status == "completed" {
			break // it finished just before the kill
		}
		if u.Status != "interrupted" || u.FinishedAt != nil || kills > 100 {
			t.Fatalf("after kill %d: %+v", kills, u)
		}
		e.resume(id)
	}
	app.Process.Signal(syscall.SIGTERM)
	app.Wait()

	if kills < 2 {
		t.Errorf("only %d kills happened mid-update", kills)
	}
	app = runApp()
	defer func() { app.Process.Signal(syscall.SIGTERM); app.Wait() }()
	u := e.get(id)
	if u.Status != "completed" || u.BatchesDone != len(e.feed.Batches) {
		t.Fatalf("final: %+v (feed has %d batches)", u, len(e.feed.Batches))
	}
	e.assertMatchesExpected(fake.ExpectedFor(e.feed))
	t.Logf("completed after %d kill -9 restarts", kills)
}

func startProcess(t *testing.T, cmd *exec.Cmd) {
	t.Helper()
	if testing.Verbose() {
		cmd.Stdout, cmd.Stderr = os.Stdout, os.Stderr
	}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
}

func freeAddr(t *testing.T) string {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	return ln.Addr().String()
}

func waitHTTP(t *testing.T, url string) {
	t.Helper()
	for i := 0; i < 200; i++ {
		if resp, err := http.Get(url); err == nil {
			resp.Body.Close()
			if resp.StatusCode == http.StatusOK {
				return
			}
		}
		time.Sleep(25 * time.Millisecond)
	}
	t.Fatal(fmt.Sprintf("%s did not come up", url))
}
