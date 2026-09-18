package splitter

import (
	"regexp"
	"strings"
)

type Split struct {
	Text       string
	IsSentence bool
	TokenSize  int
}

type SplitFunc func(text string) []string

type SentenceSplitter struct {
	ChunkSize        int
	Overlap          int
	SplitFuncs       []SplitFunc
	SubSentenceFuncs []SplitFunc
}

func NewSentenceSpliter(chunksize, overlap int) *SentenceSplitter {
	return &SentenceSplitter{
		ChunkSize:        chunksize,
		Overlap:          overlap,
		SplitFuncs:       []SplitFunc{SplitInParagraph},
		SubSentenceFuncs: []SplitFunc{SplitByRegex},
	}
}

var chunkingRegex = regexp.MustCompile(
	`[^,.;。？！]+[,.;。？！]?|[,.;。？！]`,
)

func SplitInParagraph(text string) []string {
	texts := strings.Split(text, "\n\n")
	return texts
}

func SplitByRegex(text string) []string {
	return chunkingRegex.FindAllString(text, -1)
}

func (s *SentenceSplitter) Split(text string) []Split {
	splits := make([]Split, 0)
	if len(text) <= s.ChunkSize {
		splits = append(splits, Split{
			Text:       text,
			IsSentence: false,
			TokenSize:  len(text),
		})
		return splits
	}
	textSplits, isSentence := s.GetSplitsByFuncs(text)
	for _, split := range textSplits {
		tokenSize := len(split)
		if tokenSize <= s.ChunkSize {
			splits = append(splits, Split{
				Text:       split,
				IsSentence: isSentence,
				TokenSize:  len(split),
			})
			continue
		}
		if len(textSplits) == 1 {
			splits = append(splits, Split{
				Text:       split,
				IsSentence: isSentence,
				TokenSize:  len(split),
			})
			continue
		}
		recursiveSplits := s.Split(split)
		splits = append(splits, recursiveSplits...)
	}
	return splits
}

func (s *SentenceSplitter) GetSplitsByFuncs(text string) ([]string, bool) {
	for _, splitFunc := range s.SplitFuncs {
		splits := splitFunc(text)
		if len(splits) > 1 {
			return splits, true
		}
	}

	var splits []string

	for _, splitFunc := range s.SubSentenceFuncs {
		splits = splitFunc(text)
		if len(splits) > 1 {
			break
		}
	}
	return splits, false
}
