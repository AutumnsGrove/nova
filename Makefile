.PHONY: test test-full race race-splash splash fuzz-short fmt vet lint vuln build build-notui accept ci importcheck matrixcheck-all linecount-all

test:
	go test -short -p 2 ./...

test-full:
	go test -p 2 ./...

# splashgif rasterises 150 frames for every test, so -race there costs minutes
# and finds nothing (the package is pure rendering). Keep it out of the fast
# gate; the ci workflow runs `race-splash` in a parallel job.
slow-pkg = /tui/splash/splashgif

race:
	go test -race -p 2 $$(go list ./... | grep -v '$(slow-pkg)')

race-splash:
	go test -race -timeout 25m -p 2 ./tui/splash/splashgif

# The pull request gate runs the same package without -race: the renderer takes
# 25s that way and seven minutes with the detector, which finds nothing in a
# package that starts no goroutine. Use race-splash when you touched tui/splash.
splash:
	go test -p 2 ./tui/splash/splashgif

# gofmt -l exits 0 even when it lists files, so fail on any output.
fmt:
	@out="$$(gofmt -l .)"; if [ -n "$$out" ]; then echo "gofmt needed:"; echo "$$out"; exit 1; fi

vet:
	go vet ./...

# The linters are pinned here rather than in go.mod: they are build tools, not
# dependencies, and a tools entry would put honnef.co/go/tools into the import
# allow-list (D46) and into every consumer's module graph. Pinning here also
# means a local run and the workflow run the same version.
staticcheck = honnef.co/go/tools/cmd/staticcheck@v0.8.2
govulncheck = golang.org/x/vuln/cmd/govulncheck@v1.9.0

lint:
	go run $(staticcheck) ./...

# Not part of `ci`: govulncheck downloads its advisory database, and the gate has
# to work offline. ci.yml runs it on every pull request and weekly.
vuln:
	go run $(govulncheck) ./...

# Every package holding a fuzz target. The list is derived rather than written
# down, so a new target cannot be forgotten and a moved one cannot go stale.
fuzz-pkgs = $(sort $(dir $(shell grep -rl '^func Fuzz' --include='*_test.go' .)))

# A smoke run: ten seconds per target, the same shape fuzz.yml runs for a minute
# each. A green run is a smoke alarm, not a proof that the parser is safe, and
# anything longer belongs to a deliberate fuzz campaign rather than a build.
fuzz-short:
	@test -n "$(fuzz-pkgs)" || { echo "no fuzz targets found"; exit 1; }
	@for pkg in $(fuzz-pkgs); do \
		for target in $$(go test -list '^Fuzz' $$pkg | sed -n '/^Fuzz/p'); do \
			echo "fuzzing $$target in $$pkg"; \
			go test -run '^$$' -fuzz "^$$target$$" -fuzztime=10s $$pkg || exit 1; \
			done; \
		done

build:
	go build ./...

build-notui:
	go build -tags notui ./...

importcheck:
	go run ./scripts/importcheck

accept:
	go run ./scripts/accept $(C)

ci: fmt vet lint build build-notui race importcheck matrixcheck-all linecount-all

# The chunk specs are archived (docs/archive/chunks): the build-out is finished,
# so every spec is a regression contract. Each matrix row must still have a
# test, and each package must stay inside its line budget. An empty list is an
# error, so a moved or renamed directory can never turn these guards into no-ops.
chunk-specs = $(sort $(wildcard docs/archive/chunks/C*.md))

matrixcheck-all:
	@test -n "$(chunk-specs)" || { echo "no chunk specs found under docs/archive/chunks"; exit 1; }
	@for spec in $(chunk-specs); do \
		pkgs=$$(sed -n 's/^packages: \[\(.*\)\]/\1/p' "$$spec" | head -1 | tr -d ' ' | tr ',' ' '); \
		go run ./scripts/matrixcheck "$$spec" $$pkgs || exit 1; \
	done

linecount-all:
	@test -n "$(chunk-specs)" || { echo "no chunk specs found under docs/archive/chunks"; exit 1; }
	@for spec in $(chunk-specs); do \
		go run ./scripts/linecount "$$spec" || exit 1; \
	done
