package profiler

import (
	"strings"
	"sync"

	"github.com/pkoukk/tiktoken-go"
)

const tokenizerEncoding = tiktoken.MODEL_CL100K_BASE

var (
	tokenizerOnce sync.Once
	tokenizerInst *tiktoken.Tiktoken
	tokenizerErr  error
)

func EstimateTokens(input string) int {
	trimmed := strings.TrimSpace(input)
	if trimmed == "" {
		return 0
	}
	tokenizerOnce.Do(func() {
		tokenizerInst, tokenizerErr = tiktoken.GetEncoding(tokenizerEncoding)
	})
	if tokenizerErr != nil || tokenizerInst == nil {
		return 0
	}
	return len(tokenizerInst.Encode(trimmed, nil, nil))
}
