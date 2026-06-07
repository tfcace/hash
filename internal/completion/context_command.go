package completion

import "context"

type commandRunner func(context.Context, string, ...string) ([]string, error)

type commandRunResult struct {
	lines []string
	err   error
}

func runCommandUntilContext(
	ctx context.Context,
	run commandRunner,
	name string,
	args ...string,
) ([]string, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	done := make(chan commandRunResult, 1)
	go func() {
		lines, err := run(ctx, name, args...)
		done <- commandRunResult{lines: lines, err: err}
	}()

	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case result := <-done:
		return result.lines, result.err
	}
}

type processListFunc func(context.Context) ([]processInfo, error)

type processListResult struct {
	processes []processInfo
	err       error
}

func listProcessesUntilContext(ctx context.Context, list processListFunc) ([]processInfo, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	done := make(chan processListResult, 1)
	go func() {
		processes, err := list(ctx)
		done <- processListResult{processes: processes, err: err}
	}()

	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case result := <-done:
		return result.processes, result.err
	}
}

type stringListFunc func(context.Context) ([]string, error)

type stringListResult struct {
	values []string
	err    error
}

func listStringsUntilContext(ctx context.Context, list stringListFunc) ([]string, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	done := make(chan stringListResult, 1)
	go func() {
		values, err := list(ctx)
		done <- stringListResult{values: values, err: err}
	}()

	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case result := <-done:
		return result.values, result.err
	}
}
