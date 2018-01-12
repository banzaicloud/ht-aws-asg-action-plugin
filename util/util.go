package util

import (
	"sort"
	"strconv"
	"strings"

	"github.com/banzaicloud/spot-recommender/recommender"
)

type ByCostScore []recommender.InstanceTypeInfo

func (a ByCostScore) Len() int      { return len(a) }
func (a ByCostScore) Swap(i, j int) { a[i], a[j] = a[j], a[i] }
func (a ByCostScore) Less(i, j int) bool {
	costScore1, _ := strconv.ParseFloat(strings.Split(a[i].CostScore, " ")[0], 32)
	costScore2, _ := strconv.ParseFloat(strings.Split(a[j].CostScore, " ")[0], 32)
	return costScore1 < costScore2
}

func SelectCheapestRecommendation(recommendations []recommender.InstanceTypeInfo) recommender.InstanceTypeInfo {
	r := SelectCheapestRecommendations(recommendations, 1)
	return r[0]
}

func SelectCheapestRecommendations(recommendations []recommender.InstanceTypeInfo, nrOfInstances int64) []recommender.InstanceTypeInfo {
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