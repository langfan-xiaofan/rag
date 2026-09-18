package bm25

import (
	"math"
	"rag/utils/jieba"

	"github.com/cloudwego/eino/schema"
)

func BM25(docs []*schema.Document, query string) []*schema.Document {
	k1 := 1.2
	b := 0.75
	length := 0.0
	queryWords := UniqueQueryWords(jieba.JieBa(query))
	invertedIndex := map[string]map[string]float64{}
	docLengths := make(map[string]float64)
	for _, doc := range docs {
		words := jieba.JieBa(doc.Content)
		length += float64(len(words))
		docLengths[doc.ID] = float64(len(words))
		for _, word := range words {
			if _, ok := invertedIndex[word]; !ok {
				invertedIndex[word] = make(map[string]float64)
			}

			invertedIndex[word][doc.ID]++
		}
	}
	N := float64(len(docs))
	avgdl := length / N
	maxBM25 := 0.0
	minBM25 := math.MaxFloat64
	for i, doc := range docs {
		score := 0.0
		for _, q := range queryWords {
			if invertedIndex[q] == nil {
				continue
			}
			//n(q)
			n := float64(len(invertedIndex[q]))

			//TF(q,D)
			tf := TF(q, doc.ID, invertedIndex)

			if tf == 0 {
				continue
			}

			idf := IDF(N, n)

			//|D|
			dl := docLengths[doc.ID]

			tfscore := (tf * (k1 + 1)) / (tf + k1*(1-b+b*(dl/avgdl)))
			score += idf * tfscore
		}
		if score < minBM25 {
			minBM25 = score
		}
		if score > maxBM25 {
			maxBM25 = score
		}
		docs[i].MetaData["score"] = score
	}
	for idx := range docs {
		score := docs[idx].MetaData["score"].(float64)
		norm := 0.0 // max==min（所有 doc 同分/无命中/单条候选）时统一记 0，避免除零产生 NaN
		if maxBM25 > minBM25 {
			norm = (score - minBM25) / (maxBM25 - minBM25)
		}
		docs[idx].MetaData["score"] = norm
		docs[idx].WithScore(docs[idx].Score() + norm)
	}
	return docs
}

func IDF(N, n float64) float64 {
	return math.Log((N-n+0.5)/(n+0.5) + 1)
}

func TF(word, d string, invertedIndex map[string]map[string]float64) float64 {
	return float64(invertedIndex[word][d])
}

func UniqueQueryWords(queryWords []string) []string {
	temp := make([]string, 0)
	seen := make(map[string]bool)
	for _, word := range queryWords {
		if _, ok := seen[word]; ok {
			continue
		}
		seen[word] = true
		temp = append(temp, word)
	}
	return temp
}
