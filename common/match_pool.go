package common

import "strings"

const (
	KnockdownMatchRegion         = "vs_19279" // Native custom region 0x4B4F.
	KnockdownPoolProtocolVersion = 2          // Continuous rules and canonical winner completion.
)

func IsPublicMKWMatchType(matchType string) bool {
	return matchType == "0" || matchType == "1"
}

// NormalizeMKWMatchRegion merges only stock continental regions. Custom
// regions, including the reserved Knockdown pool, keep their exact identity.
func NormalizeMKWMatchRegion(region string) string {
	if len(region) == 4 && (strings.HasPrefix(region, "vs_") || strings.HasPrefix(region, "bt_")) && region[3] >= '0' && region[3] < '6' {
		return region[:2]
	}
	return region
}
