.PHONY: test test-full race race-splash fmt vet build build-notui accept ci importcheck matrixcheck-all linecount-all

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

# gofmt -l exits 0 even when it lists files, so fail on any output.
fmt:
	@out="$$(gofmt -l .)"; if [ -n "$$out" ]; then echo "gofmt needed:"; echo "$$out"; exit 1; fi

vet:
	go vet ./...

build:
	go build ./...

build-notui:
	go build -tags notui ./...

importcheck:
	go run ./scripts/importcheck

accept:
	go run ./scripts/accept $(C)

ci: fmt vet build build-notui race importcheck matrixcheck-all linecount-all

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
