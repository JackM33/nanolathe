// Repository-level guards for the retail runtime.
//
// These tests deliberately inspect production source instead of importing the
// packages under test.  That keeps the guard independent of runtime wiring and
// lets it catch an architectural dependency before a new feature exercises it.

package architecture

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"testing"
)

// authoritativeDirs is the simulation side of the architecture boundary.
// Client, HUD, presentation, and audio are host/device edges and are
// intentionally outside this list.  The list follows the ownership boundaries
// in [01 §1] and the package graph in the repository architecture summary.
//
// internal/headless is deliberately NOT here even though it drives the session.
// It is a host/report edge: its report builder ranges a map to name packages,
// and the simulation benchmark's scene and timing records hold float64s that
// never reach simulation state.  Admitting it would mean allowlisting all of
// that, which buys a weaker guard than leaving the edge outside the boundary.
//
// internal/aikit and mods/aikit ARE here: a Modern computer player's
// controller and the rule set that binds it issue orders from phase 5, so
// every audit that guards the simulation — forbidden imports, float64, map
// order, fused arithmetic, goroutines — reads them too. The Modern AI
// exception of INVARIANTS I4 is a private generator the controller owns, and
// the asynchronous host's worker is the one justified goroutine carve-out
// (authoritativeGoroutines below); neither relaxes any audit for another
// package.
//
// mods/example is here for the same reason: it is a registered, selectable
// rule set whose order answer runs inside the tick. Nothing in the session's
// imports can reach a package under mods/ (the dependency runs the other
// way), so a rule set joins this list by hand when it is added.
//
// internal/netproto is here as internal/version is: it writes and reads the
// command payloads phase 1 applies and every identity a lockstep battle is
// admitted on (DESIGN_MULTIPLAYER §7.4, §8.2, §14), so every audit — imports,
// float64, map order, goroutines — reads it too. It holds no float at all.
var authoritativeDirs = []string{
	"internal/ai",
	"internal/aikit",
	"internal/clock",
	"internal/cob",
	"internal/combat",
	"internal/construction",
	"internal/economy",
	"internal/effects",
	"internal/features",
	"internal/frame",
	"internal/mission",
	"internal/model",
	"internal/movement",
	"internal/netproto",
	"internal/orders",
	"internal/path",
	"internal/pool",
	"internal/save",
	"internal/session",
	"internal/sim",
	"internal/survival",
	"internal/triggers",
	"internal/units",
	"internal/version",
	"internal/visibility",
	"internal/world",
	"mods/aikit",
	"mods/example",
}

// loadTimeSimulationInputDirs are the load-time packages whose output the
// simulation reads: the mounted files (vfs), the parsers that turn them into
// numbers (formats), the catalog compiler, its mutators and content profiles
// (internal/content), the Community tables (internal/community) and gameplay
// mode selection (internal/gameplay). None runs inside a tick, but every host
// in a lockstep battle compiles its own catalog from them, so a value they
// compute differently on another processor becomes a different simulation.
//
// They get the numeric guards — TestAuthoritativeNumericPortability and both
// fused-arithmetic tests — and nothing else. A floating-to-integer conversion
// out of range, a library approximation and a fused multiply-add are the three
// ways the same source compiles to different answers on arm64, amd64 and
// amd64-v3, and they matter wherever a simulation input is computed. The
// other audits over authoritativeDirs — float64 scope, map order, goroutines
// and imports — protect what a tick does; load-time code legitimately ranges
// catalog maps and parses binary64, and the cinematic decoder imports time.
// Admitting it to authoritativeDirs would mean allowlisting all of that for a
// weaker guard.
//
// The formats tree comes in whole, its cinematic decoder included; every
// parser there is a candidate input. TestNumericGuardsCoverTheSessionClosure
// keeps this list complete against the session's imports.
var loadTimeSimulationInputDirs = []string{
	"formats",
	"internal/community",
	"internal/content",
	"internal/gameplay",
	"vfs",
}

// sessionClosureHostEdges are the in-module packages internal/session imports
// that are neither authoritative nor simulation inputs, each with the reason.
// The numeric guards do not read them; their arithmetic is presentation.
var sessionClosureHostEdges = map[string]string{
	"internal/audio":   "sound playback, mixing and positional gain; the session emits cues into it",
	"internal/camera":  "view geometry, reached through internal/hud",
	"internal/hud":     "interface layout and drawing; the selection-group and build-page helpers the session applies from commands are integer-only",
	"internal/input":   "pointer and key state, reached through internal/hud",
	"internal/palette": "display palette rebuilds, reached through internal/hud",
	"internal/render":  "draw lists, reached through internal/hud",
}

// numericGuardDirs is the scope of the numeric-portability and fused-arithmetic
// guards: the authoritative packages and the load-time simulation inputs.
func numericGuardDirs() []string {
	return append(append([]string(nil), authoritativeDirs...), loadTimeSimulationInputDirs...)
}

// TestNumericGuardsCoverTheSessionClosure fails when internal/session comes to
// import an in-module package that is neither guarded nor named as a host edge,
// so a new load-time input cannot slip outside the numeric guards.
func TestNumericGuardsCoverTheSessionClosure(t *testing.T) {
	root := repositoryRoot(t)
	module := modulePath(t, root)
	command := exec.Command("go", "list", "-deps", "./internal/session")
	command.Dir = root
	output, err := command.Output()
	if err != nil {
		t.Fatalf("list session dependencies: %v", err)
	}
	guarded := numericGuardDirs()
	seen := map[string]bool{}
	var failures []string
	for _, importPath := range strings.Fields(string(output)) {
		relative, ok := strings.CutPrefix(importPath, module+"/")
		if !ok || isImportPathUnder(importPath, guarded) {
			continue
		}
		if _, edge := sessionClosureHostEdges[relative]; edge {
			seen[relative] = true
			continue
		}
		failures = append(failures, relative+" (in the session's imports but neither numerically guarded nor a named host edge)")
	}
	for relative := range sessionClosureHostEdges {
		if !seen[relative] {
			failures = append(failures, relative+" (stale host edge: the session no longer imports it)")
		}
	}
	if len(failures) != 0 {
		sort.Strings(failures)
		t.Fatalf("numeric guard scope is out of date: %s", strings.Join(failures, "; "))
	}
}

// TestAuthoritativePackagesDoNotImportHostOrNondeterministicRuntime checks
// that simulation packages cannot acquire device/window APIs, wall-clock
// services, or an unrelated random stream.  It also keeps the executable,
// client, and test/clean-room scaffolding outside the authoritative graph.
// Ebitengine remains permitted at the client/audio edge only; simulation
// randomness is the two streams specified by [01 §7].
func TestAuthoritativePackagesDoNotImportHostOrNondeterministicRuntime(t *testing.T) {
	root := repositoryRoot(t)
	violations := scanAuthoritativeFiles(t, root, func(path string, file *ast.File, fset *token.FileSet) []string {
		var found []string
		for _, spec := range file.Imports {
			importPath, err := strconv.Unquote(spec.Path.Value)
			if err != nil {
				continue
			}
			if forbiddenRuntimeImport(importPath) {
				pos := fset.Position(spec.Pos())
				found = append(found, formatViolation(root, path, pos.Line, importPath))
			}
		}
		return found
	})
	if len(violations) != 0 {
		sort.Strings(violations)
		t.Fatalf("authoritative package imports host or nondeterministic runtime: %s", strings.Join(violations, "; "))
	}
}

// authoritativeGoroutines is the goroutine allowlist for the authoritative
// packages: the files that may contain a `go` statement, each with the reason
// its goroutine cannot make the simulation depend on scheduling (I1). It is
// shrink-only; an entry whose file no longer starts a goroutine is stale.
var authoritativeGoroutines = map[string]string{
	"internal/aikit/host.go": "the asynchronous host's worker runs one brain think on a copy the simulation thread built; the thread hands it the observation, joins it at the fixed reaction deadline and applies its commands there, so the tick the commands land on and their content are the synchronous host's (DESIGN_GAMEPLAY_RULES §5)",
	// The Modern path batch helpers (INVARIANTS I1 "Parallel simulation exception").
	"internal/path/batch_workers.go": "a Modern path batch's helpers resume searches whose membership, order and pop caps the simulation thread fixed before the round, each on its own storage against inputs nothing writes until the round joins; the thread waits for every helper and then charges and publishes in admission order, so no result depends on which goroutine ran a search or how many took part (DESIGN_MOVEMENT_PATH \"Modern parallel path batches\")",
	// The publication unit channel (INVARIANTS I1 "Parallel simulation exception").
	"internal/session/publish_parallel.go": "publication workers build the frame's unit channel from committed state nothing writes until the frame is committed; every order-dependent choice (slot order, identities, queue indices, the chunk split by unit count) is made before they start, each chunk writes only its own elements, and the frame is committed after the join, so the frame is the serial build's (INVARIANTS I1 \"Parallel simulation exception\", DESIGN_RUNTIME_DETERMINISM §2.5)",
}

// TestAuthoritativePackagesStartNoGoroutines keeps goroutine scheduling out of
// the simulation (I1): no authoritative file starts one unless it is named
// above with the argument that makes its result independent of scheduling.
func TestAuthoritativePackagesStartNoGoroutines(t *testing.T) {
	root := repositoryRoot(t)
	seen := map[string]bool{}
	violations := scanAuthoritativeFiles(t, root, func(path string, file *ast.File, fset *token.FileSet) []string {
		relative, err := filepath.Rel(root, path)
		if err != nil {
			relative = path
		}
		relative = filepath.ToSlash(relative)
		var found []string
		ast.Inspect(file, func(node ast.Node) bool {
			statement, ok := node.(*ast.GoStmt)
			if !ok {
				return true
			}
			if _, allowed := authoritativeGoroutines[relative]; allowed {
				seen[relative] = true
				return true
			}
			found = append(found, formatViolation(root, path, fset.Position(statement.Pos()).Line, "go statement"))
			return true
		})
		return found
	})
	for path := range authoritativeGoroutines {
		if !seen[path] {
			violations = append(violations, path+" (stale goroutine allowance)")
		}
	}
	if len(violations) != 0 {
		sort.Strings(violations)
		t.Fatalf("authoritative package starts a goroutine outside the allowlist (I1): %s", strings.Join(violations, "; "))
	}
}

func forbiddenRuntimeImport(path string) bool {
	switch path {
	case "crypto/rand", "math/rand", "time":
		return true
	}
	for _, prefix := range []string{
		"github.com/hajimehoshi/ebiten/v2",
		"github.com/nanolathe-gg/nanolathe/cmd/nanolathe",
		"github.com/nanolathe-gg/nanolathe/internal/client",
		"github.com/nanolathe-gg/nanolathe/internal/render",
		"github.com/nanolathe-gg/nanolathe/internal/cleanroom",
		"github.com/nanolathe-gg/nanolathe/internal/testsupport",
	} {
		if path == prefix || strings.HasPrefix(path, prefix+"/") {
			return true
		}
	}
	return false
}

type fileVisitor func(path string, file *ast.File, fset *token.FileSet) []string

func scanAuthoritativeFiles(t *testing.T, root string, visit fileVisitor) []string {
	t.Helper()
	return scanSourceFiles(t, root, authoritativeDirs, visit)
}

// scanSourceFiles visits every non-test Go file at or below dirs.
func scanSourceFiles(t *testing.T, root string, dirs []string, visit fileVisitor) []string {
	t.Helper()
	var violations []string
	for _, relativeDir := range dirs {
		dir := filepath.Join(root, filepath.FromSlash(relativeDir))
		err := guardWalkDir(dir, func(path string, entry os.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".go") || strings.HasSuffix(entry.Name(), "_test.go") {
				return nil
			}
			fset := token.NewFileSet()
			file, parseErr := parser.ParseFile(fset, path, nil, 0)
			if parseErr != nil {
				return parseErr
			}
			violations = append(violations, visit(path, file, fset)...)
			return nil
		})
		if err != nil {
			t.Fatalf("scan %s: %v", relativeDir, err)
		}
	}
	return violations
}

func repositoryRoot(t *testing.T) string {
	t.Helper()
	directory, err := os.Getwd()
	if err != nil {
		t.Fatalf("get working directory: %v", err)
	}
	for {
		if _, err := os.Stat(filepath.Join(directory, "go.mod")); err == nil {
			return directory
		}
		parent := filepath.Dir(directory)
		if parent == directory {
			t.Fatal("could not locate repository go.mod")
		}
		directory = parent
	}
}

func formatViolation(root, path string, line int, subject string) string {
	relative, err := filepath.Rel(root, path)
	if err != nil {
		relative = path
	}
	return relative + ":" + strconv.Itoa(line) + " (" + subject + ")"
}
