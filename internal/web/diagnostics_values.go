package web

import (
	"fmt"
	"strconv"
	"time"

	"github.com/sglavoie/brainnotes/internal/frontmatter"
)

func countDiagnosticValue(value any, counts map[string]int) {
	switch typed := value.(type) {
	case nil:
		return
	case string:
		counts[typed]++
	case []any:
		for _, item := range typed {
			countDiagnosticValue(item, counts)
		}
	case []string:
		for _, item := range typed {
			counts[item]++
		}
	case map[string]any:
		return
	case time.Time:
		counts[frontmatter.FormatTimestamp(typed)]++
	case bool:
		counts[strconv.FormatBool(typed)]++
	case int:
		counts[strconv.Itoa(typed)]++
	case int64:
		counts[strconv.FormatInt(typed, 10)]++
	case float64:
		counts[strconv.FormatFloat(typed, 'g', -1, 64)]++
	default:
		counts[fmt.Sprintf("%v", typed)]++
	}
}
