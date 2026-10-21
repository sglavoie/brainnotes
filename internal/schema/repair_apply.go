package schema

import (
	"reflect"
	"time"

	"github.com/sglavoie/brainnotes/internal/config"
	"github.com/sglavoie/brainnotes/internal/frontmatter"
)

// ApplyRepairAction applies one planned repair while retaining the timestamp
// spelling that frontmatter.Map.Set cannot infer. Declared date and datetime
// properties follow their schema kind; undeclared values retain the spelling
// of each unchanged timestamp that survives the repair.
func ApplyRepairAction(
	front *frontmatter.Map,
	action RepairAction,
	definitions map[string]config.PropertyDefinition,
) {
	if front == nil {
		return
	}

	before, _ := front.Get(action.Property)
	scalarDateOnly, scalarWasTime := scalarTimeSpelling(front, action.Property, before)
	listSpellings := snapshotListTimeSpellings(front, action.Property, before)

	front.Set(action.Property, action.NewValue)

	if definition, ok := definitions[action.Property]; ok {
		kind := definition.ScalarKind()
		if kind == config.KindDate || kind == config.KindDatetime {
			stampDeclaredTimeSpelling(front, action.Property, action.NewValue, kind == config.KindDate)
		}
		return
	}

	if scalarWasTime {
		if after, ok := action.NewValue.(time.Time); ok && reflect.DeepEqual(before, after) {
			front.SetTimeSpelling(action.Property, scalarDateOnly)
		}
		return
	}
	preserveRetainedListTimeSpellings(front, action.Property, action.NewValue, listSpellings)
}

type listTimeSpelling struct {
	value    any
	dateOnly bool
	matched  bool
}

func scalarTimeSpelling(front *frontmatter.Map, property string, value any) (bool, bool) {
	if _, ok := value.(time.Time); !ok {
		return false, false
	}
	return front.TimeIsDateOnly(property), true
}

func snapshotListTimeSpellings(front *frontmatter.Map, property string, value any) []listTimeSpelling {
	list, ok := value.([]any)
	if !ok {
		return nil
	}
	spellings := make([]listTimeSpelling, 0, len(list))
	for index, item := range list {
		if _, ok := item.(time.Time); !ok {
			continue
		}
		spellings = append(spellings, listTimeSpelling{
			value:    item,
			dateOnly: front.ListTimeIsDateOnly(property, index),
		})
	}
	return spellings
}

func stampDeclaredTimeSpelling(front *frontmatter.Map, property string, value any, dateOnly bool) {
	if _, ok := value.(time.Time); ok {
		front.SetTimeSpelling(property, dateOnly)
		return
	}
	list, ok := value.([]any)
	if !ok {
		return
	}
	for index, item := range list {
		if _, ok := item.(time.Time); ok {
			front.SetListTimeSpelling(property, index, dateOnly)
		}
	}
}

func preserveRetainedListTimeSpellings(
	front *frontmatter.Map,
	property string,
	value any,
	spellings []listTimeSpelling,
) {
	list, ok := value.([]any)
	if !ok || len(spellings) == 0 {
		return
	}
	for outputIndex, item := range list {
		if _, ok := item.(time.Time); !ok {
			continue
		}
		for inputIndex := range spellings {
			candidate := &spellings[inputIndex]
			if candidate.matched || !reflect.DeepEqual(candidate.value, item) {
				continue
			}
			candidate.matched = true
			front.SetListTimeSpelling(property, outputIndex, candidate.dateOnly)
			break
		}
	}
}
