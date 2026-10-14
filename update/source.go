package update

import (
	"context"
	"fmt"
	"strings"
)

// runSource rebuilds the binary from a local checkout with the go toolchain.
func runSource(ctx context.Context, opt Options, dir string) (Result, error) {
	root, ok := Detect(dir)
	if !ok {
		return Result{}, fmt.Errorf("%s is not a nova source checkout", dir)
	}
	if _, err := opt.LookPath("go"); err != nil {
		return Result{}, fmt.Errorf("the go toolchain is required to build from source: %w", err)
	}
	res := Result{Mode: Source, To: "source"}
	if opt.Check {
		res.Message = fmt.Sprintf("source checkout at %s; run `nova update` to rebuild", root)
		return res, nil
	}
	staged, err := stage(opt.Target)
	if err != nil {
		return Result{}, err
	}
	defer staged.cleanup()
	args := []string{"build", "-o", staged.path}
	if opt.Flavour == "min" {
		args = append(args, "-tags", "notui")
	}
	args = append(args, "./cmd/nova")
	env := append(append([]string(nil), opt.Env...), "CGO_ENABLED=0")
	if out, err := opt.Exec(ctx, root, env, "go", args...); err != nil {
		return Result{}, fmt.Errorf("go build: %w: %s", err, strings.TrimSpace(string(out)))
	}
	if err := staged.commit(ctx, opt); err != nil {
		return Result{}, err
	}
	res.Changed = true
	res.Message = fmt.Sprintf("rebuilt nova from %s", root)
	return res, nil
}
