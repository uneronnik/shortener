package link

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"testing"
	"time"
)

type fakeRepository struct {
	save            func(context.Context, Link) error
	findByCode      func(context.Context, string) (Link, error)
	incrementClicks func(context.Context, string) (string, error)
}

func (r fakeRepository) Save(ctx context.Context, link Link) error {
	return r.save(ctx, link)
}

func (r fakeRepository) FindByCode(ctx context.Context, code string) (Link, error) {
	return r.findByCode(ctx, code)
}

func (r fakeRepository) IncrementClicks(ctx context.Context, code string) (string, error) {
	return r.incrementClicks(ctx, code)
}

func TestServiceShorten(t *testing.T) {
	storageErr := errors.New("storage unavailable")
	randomErr := errors.New("random source unavailable")
	tests := []struct {
		name         string
		url          string
		saveErrors   []error
		generateErr  error
		wantErr      error
		wantGenerate int
		wantSave     int
	}{
		{name: "success", url: "https://go.dev", wantGenerate: 1, wantSave: 1},
		{name: "invalid URL", url: "ftp://go.dev", wantErr: ErrInvalidURL},
		{name: "retry collision", url: "https://go.dev", saveErrors: []error{fmt.Errorf("insert: %w", ErrCodeTaken)}, wantGenerate: 2, wantSave: 2},
		{name: "success on last attempt", url: "https://go.dev", saveErrors: []error{ErrCodeTaken, ErrCodeTaken}, wantGenerate: 3, wantSave: 3},
		{name: "exhaust collisions", url: "https://go.dev", saveErrors: []error{ErrCodeTaken, ErrCodeTaken, ErrCodeTaken}, wantErr: ErrCodeTaken, wantGenerate: 3, wantSave: 3},
		{name: "storage failure", url: "https://go.dev", saveErrors: []error{storageErr}, wantErr: storageErr, wantGenerate: 1, wantSave: 1},
		{name: "storage canceled", url: "https://go.dev", saveErrors: []error{context.Canceled}, wantErr: context.Canceled, wantGenerate: 1, wantSave: 1},
		{name: "storage failure after collision", url: "https://go.dev", saveErrors: []error{ErrCodeTaken, storageErr}, wantErr: storageErr, wantGenerate: 2, wantSave: 2},
		{name: "generator failure", url: "https://go.dev", generateErr: randomErr, wantErr: randomErr, wantGenerate: 1},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			codes := []string{"aaaaaaa", "bbbbbbb", "ccccccc"}
			generated := 0
			var saved []Link
			repo := fakeRepository{save: func(gotCtx context.Context, link Link) error {
				if gotCtx != ctx {
					t.Fatal("Save did not receive the request context")
				}
				saved = append(saved, link)
				if len(saved) <= len(tt.saveErrors) {
					return tt.saveErrors[len(saved)-1]
				}
				return nil
			}}
			service := NewService(repo, func() (string, error) {
				generated++
				if generated > len(codes) {
					t.Fatal("too many generation attempts")
				}
				return codes[generated-1], tt.generateErr
			})
			before := time.Now()
			got, err := service.Shorten(ctx, tt.url)
			after := time.Now()
			if !errors.Is(err, tt.wantErr) {
				t.Fatalf("Shorten error = %v, want %v", err, tt.wantErr)
			}
			if generated != tt.wantGenerate || len(saved) != tt.wantSave {
				t.Fatalf("generate/save calls = %d/%d, want %d/%d", generated, len(saved), tt.wantGenerate, tt.wantSave)
			}
			for i, link := range saved {
				if link.URL != tt.url || link.Code != codes[i] || link.Clicks != 0 {
					t.Errorf("saved link %d = %+v", i, link)
				}
				if link.CreatedAt.Before(before) || link.CreatedAt.After(after) {
					t.Errorf("CreatedAt = %v, want current time", link.CreatedAt)
				}
			}
			if tt.wantErr != nil {
				if !reflect.DeepEqual(got, Link{}) {
					t.Errorf("failed Shorten returned %+v, want zero Link", got)
				}
			} else if !reflect.DeepEqual(got, saved[len(saved)-1]) {
				t.Errorf("Shorten = %+v, want saved link %+v", got, saved[len(saved)-1])
			}
		})
	}
}

func TestServiceShortenCanceled(t *testing.T) {
	for _, cancelOnCollision := range []bool{false, true} {
		t.Run(fmt.Sprintf("cancel on collision=%t", cancelOnCollision), func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			if !cancelOnCollision {
				cancel()
			}
			generated, saved := 0, 0
			repo := fakeRepository{save: func(context.Context, Link) error {
				saved++
				cancel()
				return ErrCodeTaken
			}}
			service := NewService(repo, func() (string, error) {
				generated++
				return "abc1234", nil
			})
			got, err := service.Shorten(ctx, "https://go.dev")
			wantCalls := 0
			if cancelOnCollision {
				wantCalls = 1
			}
			if !errors.Is(err, context.Canceled) || generated != wantCalls || saved != wantCalls {
				t.Fatalf("Shorten error = %v, generate/save = %d/%d; want canceled, %d/%d", err, generated, saved, wantCalls, wantCalls)
			}
			if !reflect.DeepEqual(got, Link{}) {
				t.Errorf("canceled Shorten returned %+v, want zero Link", got)
			}
		})
	}
}

func TestServiceResolve(t *testing.T) {
	storageErr := errors.New("storage unavailable")
	for _, wantErr := range []error{nil, fmt.Errorf("update: %w", ErrNotFound), storageErr, context.Canceled} {
		t.Run(fmt.Sprint(wantErr), func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			calls := 0
			repo := fakeRepository{incrementClicks: func(gotCtx context.Context, code string) (string, error) {
				calls++
				if gotCtx != ctx || code != "abc1234" {
					t.Fatalf("IncrementClicks arguments = %v, %q", gotCtx, code)
				}
				return "https://go.dev", wantErr
			}}
			service := NewService(repo, GenerateCode)
			got, err := service.Resolve(ctx, "abc1234")
			if !errors.Is(err, wantErr) || calls != 1 {
				t.Fatalf("Resolve error = %v, calls = %d; want %v, 1", err, calls, wantErr)
			}
			if wantErr == nil && got != "https://go.dev" {
				t.Errorf("Resolve = %q, want original URL", got)
			}
			if wantErr != nil && got != "" {
				t.Errorf("failed Resolve = %q, want empty URL", got)
			}
		})
	}
}

func TestServiceStats(t *testing.T) {
	storageErr := errors.New("storage unavailable")
	want := Link{URL: "https://go.dev", Code: "abc1234", Clicks: 42, CreatedAt: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)}
	for _, wantErr := range []error{nil, fmt.Errorf("select: %w", ErrNotFound), storageErr, context.DeadlineExceeded} {
		t.Run(fmt.Sprint(wantErr), func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			calls := 0
			repo := fakeRepository{findByCode: func(gotCtx context.Context, code string) (Link, error) {
				calls++
				if gotCtx != ctx || code != want.Code {
					t.Fatalf("FindByCode arguments = %v, %q", gotCtx, code)
				}
				return want, wantErr
			}}
			service := NewService(repo, GenerateCode)
			got, err := service.Stats(ctx, want.Code)
			if !errors.Is(err, wantErr) || calls != 1 {
				t.Fatalf("Stats error = %v, calls = %d; want %v, 1", err, calls, wantErr)
			}
			if wantErr == nil && !reflect.DeepEqual(got, want) {
				t.Errorf("Stats = %+v, want %+v", got, want)
			}
			if wantErr != nil && !reflect.DeepEqual(got, Link{}) {
				t.Errorf("failed Stats = %+v, want zero Link", got)
			}
		})
	}
}
