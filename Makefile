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
	go test -race -p 2 ./tui/splash/splashgif

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

# Guard only chunks whose Status in NEXTSTEPS.md is `done`. The chunk being
# worked on is gated by `accept`; later chunks are not guarded yet. The M0
# prototype occupies some future paths, so package existence alone is not a
# reliable signal that a chunk is finished.
done-chunks = $(shell awk -F'|' '/^\| *C[0-9][0-9] */ {c=$$2; s=$$4; gsub(/ /,"",c); gsub(/ /,"",s); if (s=="done") print c}' NEXTSTEPS.md)

matrixcheck-all:
	@done="$(done-chunks)"; \
	for spec in docs/chunks/C*.md; do \
		chunk=$$(sed -n 's/^chunk: *//p' "$$spec" | head -1); \
		case " $$done " in *" $$chunk "*) ;; *) echo "skip $$spec ($$chunk not done)"; continue;; esac; \
		pkgs=$$(sed -n 's/^packages: \[\(.*\)\]/\1/p' "$$spec" | head -1 | tr -d ' ' | tr ',' ' '); \
		go run ./scripts/matrixcheck "$$spec" $$pkgs || exit 1; \
	done

linecount-all:
	@done="$(done-chunks)"; \
	for spec in docs/chunks/C*.md; do \
		chunk=$$(sed -n 's/^chunk: *//p' "$$spec" | head -1); \
		case " $$done " in *" $$chunk "*) ;; *) echo "skip $$spec ($$chunk not done)"; continue;; esac; \
		go run ./scripts/linecount "$$spec" || exit 1; \
	done
