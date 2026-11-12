.PHONY: test test-full race race-splash splash nightly fuzz-short fuzz-packages fmt vet lint vuln build build-notui build-cross tidy-diff actionlint static ci importcheck matrixcheck-all linecount-all

test:
	go test -short -p 2 ./...

test-full:
	go test -p 2 ./...

# splashgif rasterises 150 frames for every test, so -race there costs minutes
# and finds nothing (the package is pure rendering). Keep it out of the fast
# gate: the ci workflow runs `make splash` in its own job, and the release gate
# runs this with -race on every tag.
slow-pkg = /tui/splash/splashgif

race:
	go test -race -p 2 $$(go list ./... | grep -v '$(slow-pkg)')

race-splash:
	go test -race -timeout 25m -p 2 ./tui/splash/splashgif

# What nightly.yml runs. -count=2 catches a test that leaves state behind for
# the next run, and -shuffle=on catches one that only fails beside another. Go
# prints the seed, so a failure is reproducible with -shuffle=<seed>.
nightly:
	go test -race -count=2 -shuffle=on -p 2 $$(go list ./... | grep -v '$(slow-pkg)')

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
# to work offline. security.yml runs it on every pull request, on main, weekly,
# and in the release gate.
vuln:
	go run $(govulncheck) ./...

# Every package holding a fuzz target. The list is derived rather than written
# down, so a new target cannot be forgotten and a moved one cannot go stale.
fuzz-pkgs = $(sort $(dir $(shell grep -rl '^func Fuzz' --include='*_test.go' .)))

# Printed for fuzz.yml, which shards a job per line of it.
fuzz-packages:
	@test -n "$(fuzz-pkgs)" || { echo "no fuzz targets found"; exit 1; }
	@echo $(fuzz-pkgs)

# A smoke run: ten seconds per target, the same shape fuzz.yml runs for a minute
# each. A green run is a smoke alarm, not a proof that the parser is safe, and
# anything longer belongs to a deliberate fuzz campaign rather than a build.
# The target list is captured before the loop: a command substitution in a for
# list is not a command, so a failing `go test -list` there would run zero
# targets and exit 0. A fuzz job that silently tests nothing is worse than no
# fuzz job.
fuzz-short:
	@test -n "$(fuzz-pkgs)" || { echo "no fuzz targets found"; exit 1; }
	@set -eu; for pkg in $(fuzz-pkgs); do \
		listing=$$(go test -list '^Fuzz' $$pkg); \
		targets=$$(printf '%s\n' "$$listing" | sed -n '/^Fuzz/p'); \
		test -n "$$targets" || { echo "no fuzz targets in $$pkg"; exit 1; }; \
		for target in $$targets; do \
			echo "fuzzing $$target in $$pkg"; \
			go test -run '^$$' -fuzz "^$$target$$" -fuzztime=10s $$pkg; \
			done; \
		done

build:
	go build ./...

build-notui:
	go build -tags notui ./...

# The release matrix crosses four platforms, and until this target existed the
# first time two of them were compiled was on the tag that published them. They
# cannot be tested here, but they can be made to compile.
build-cross:
	GOOS=windows GOARCH=amd64 CGO_ENABLED=0 go build ./...
	GOOS=linux GOARCH=arm64 CGO_ENABLED=0 go build ./...

tidy-diff:
	go mod tidy -diff

# pi-review.yml is generated from .github/pi-review.json by the review bot, and
# its one finding is inside the script that generator writes, so it is not ours
# to fix here. Everything else in the directory is hand-written and must lint.
own-workflows = $(filter-out .github/workflows/pi-review.yml,$(wildcard .github/workflows/*.yml))
actionlint:
	@test -n "$(own-workflows)" || { echo "no workflows found"; exit 1; }
	go run github.com/rhysd/actionlint/cmd/actionlint@v1.7.12 $(own-workflows)

importcheck:
	go run ./scripts/importcheck

accept:
	go run ./scripts/accept $(C)

# Everything that does not need the race detector, in the order that reports the
# cheapest failure first. The ci workflow runs this as its own job so it does not
# wait for the race suite, and `ci` below is still the whole gate.
static: fmt vet lint tidy-diff actionlint build build-notui build-cross importcheck matrixcheck-all linecount-all

ci: static race

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
