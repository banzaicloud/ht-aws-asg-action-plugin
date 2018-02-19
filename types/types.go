package types

import (
	"sort"
	"strconv"
	"strings"
)

type Recommendation map[string][]InstanceTypeRecommendation

type InstanceTypeRecommendation struct {
	InstanceTypeName   string `json:"InstanceTypeName"`
	CurrentPrice       string `json:"CurrentPrice"`
	AvgPriceFor24Hours string `json:"AvgPriceFor24Hours"`
	OnDemandPrice      string `json:"OnDemandPrice"`
	SuggestedBidPrice  string `json:"SuggestedBidPrice"`
	CostScore          string `json:"CostScore"`
	StabilityScore     string `json:"StabilityScore"`
}

type ByCostScore []InstanceTypeRecommendation

func (a ByCostScore) Len() int      { return len(a) }
func (a ByCostScore) Swap(i, j int) { a[i], a[j] = a[j], a[i] }
func (a ByCostScore) Less(i, j int) bool {
	costScore1, _ := strconv.ParseFloat(strings.Split(a[i].CostScore, " ")[0], 32)
	costScore2, _ := strconv.ParseFloat(strings.Split(a[j].CostScore, " ")[0], 32)
	return costScore1 < costScore2
}

func SelectCheapestRecommendation(recommendations []InstanceTypeRecommendation) InstanceTypeRecommendation {
	r := SelectCheapestRecommendations(recommendations, 1)
	return r[0]
}

func SelectCheapestRecommendations(recommendations []InstanceTypeRecommendation, nrOfInstances int64) []InstanceTypeRecommendation {
	sort.Sort(sort.Reverse(ByCostScore(recommendations)))
	if nrOfInstances < 2 || len(recommendations) < 2 {
		return recommendations[:1]
	} else if nrOfInstances < 9 || len(recommendations) < 3 {
		return recommendations[:2]
	} else if nrOfInstances < 20 || len(recommendations) < 4 {
		return recommendations[:3]
	} else {
		return recommendations[:4]
	}
}
