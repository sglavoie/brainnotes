package brief

import (
	"errors"
	"fmt"
	"time"
)

// ServiceWarningCode is a stable warning class for non-fatal service outcomes.
type ServiceWarningCode string

const (
	// WarningStateUnreadable indicates that brief state could not be read safely.
	WarningStateUnreadable ServiceWarningCode = "state_unreadable"
)

// ServiceWarning is a non-fatal warning returned alongside a brief result.
type ServiceWarning struct {
	Code    ServiceWarningCode `json:"code"`
	Message string             `json:"message"`
}

// DailyResult is the composed brief plus current rolling activity.
type DailyResult struct {
	Brief    DailyBrief       `json:"brief"`
	Activity RollingActivity  `json:"activity"`
	Warnings []ServiceWarning `json:"warnings,omitempty"`
}

// AnotherResult is the result of replacing one brief slot for the current day.
type AnotherResult struct {
	Daily    DailyResult  `json:"daily"`
	Affected []SlotResult `json:"affected"`
}

// ServiceOption configures a Daily Brief service.
type ServiceOption func(*Service)

// WithServiceClock injects a deterministic clock for local-date and activity operations.
func WithServiceClock(now func() time.Time) ServiceOption {
	return func(service *Service) {
		service.now = now
	}
}

// Service composes selectors with state persistence for the daily brief lifecycle.
type Service struct {
	state    StateStore
	selector Selector
	now      func() time.Time
}

type ensuredSelection struct {
	date        LocalDate
	selection   DailySelection
	unavailable map[SlotKind]UnavailableSlot
	warnings    []ServiceWarning
}

// NewService builds the authoritative daily composition service.
func NewService(state StateStore, selector Selector, options ...ServiceOption) (*Service, error) {
	if state == nil {
		return nil, errors.New("brief: nil state store")
	}
	if selector == nil {
		return nil, errors.New("brief: nil selector")
	}
	service := &Service{
		state:    state,
		selector: selector,
		now:      time.Now,
	}
	for _, option := range options {
		if option != nil {
			option(service)
		}
	}
	return service, nil
}

// Daily returns today's brief, materializing and repairing persisted cards as needed.
func (service *Service) Daily(corpus CorpusSnapshot) (DailyResult, error) {
	now, date, err := service.nowAndDate()
	if err != nil {
		return DailyResult{}, err
	}
	ensured, err := service.ensureSelection(corpus, now, true)
	if err != nil {
		return DailyResult{}, err
	}

	activity, activityErr := service.state.RollingActivity(now)
	if activityErr != nil {
		appendWarning(&ensured.warnings, stateUnreadableWarning(activityErr))
	}
	brief := buildDailyBrief(date, ensured.selection, ensured.unavailable)
	return DailyResult{
		Brief:    brief,
		Activity: activity,
		Warnings: append([]ServiceWarning(nil), ensured.warnings...),
	}, nil
}

// Another replaces one slot for today's brief and returns all affected cards.
func (service *Service) Another(corpus CorpusSnapshot, slot SlotKind) (AnotherResult, error) {
	if !validSlotKind(slot) {
		return AnotherResult{}, fmt.Errorf("brief: unknown slot %q", slot)
	}
	now, date, err := service.nowAndDate()
	if err != nil {
		return AnotherResult{}, err
	}
	ensured, err := service.ensureSelection(corpus, now, false)
	if err != nil {
		return AnotherResult{}, err
	}
	current := cardBySlot(ensured.selection.Cards)
	updatedUnavailable := copyUnavailable(ensured.unavailable)
	replacements := make(map[SlotKind]Card)
	removed := make(map[SlotKind]struct{})
	affected := make([]SlotResult, 0, 2)

	var currentContinue *Card
	if card, ok := current[SlotContinue]; ok {
		clone := cloneCard(card)
		currentContinue = &clone
	}

	switch slot {
	case SlotContinue:
		currentCard, ok := current[SlotContinue]
		if !ok {
			affected = append(affected, unavailableSlot(SlotContinue, defaultUnavailableMessage(SlotContinue)))
			break
		}
		nextContinueResult, err := service.selectContinue(corpus, currentCard.Source.RelPath)
		if err != nil {
			return AnotherResult{}, err
		}
		nextContinue, hasContinue, err := materializeSlotCard(date, nextContinueResult)
		if err != nil {
			return AnotherResult{}, err
		}
		affected = append(affected, materializedSlotResult(nextContinueResult, nextContinue, hasContinue))
		if !hasContinue {
			updatedUnavailable[SlotContinue] = unavailableFromResult(nextContinueResult, defaultUnavailableMessage(SlotContinue))
			break
		}
		replacements[SlotContinue] = nextContinue
		nextContinueClone := cloneCard(nextContinue)
		currentContinue = &nextContinueClone

		nextConnectResult, err := service.selectConnect(corpus, currentContinue)
		if err != nil {
			return AnotherResult{}, err
		}
		nextConnect, hasConnect, err := materializeSlotCard(date, nextConnectResult)
		if err != nil {
			return AnotherResult{}, err
		}
		affected = append(affected, materializedSlotResult(nextConnectResult, nextConnect, hasConnect))
		if hasConnect {
			replacements[SlotConnect] = nextConnect
		} else {
			removed[SlotConnect] = struct{}{}
			updatedUnavailable[SlotConnect] = unavailableFromResult(nextConnectResult, defaultConnectUnavailableMessage(currentContinue != nil))
		}

	case SlotRediscover:
		nextRediscoverResult, err := service.selector.SelectRediscover(corpus, currentContinue)
		if err != nil {
			return AnotherResult{}, err
		}
		nextRediscover, hasRediscover, err := materializeSlotCard(date, nextRediscoverResult)
		if err != nil {
			return AnotherResult{}, err
		}
		affected = append(affected, materializedSlotResult(nextRediscoverResult, nextRediscover, hasRediscover))
		if hasRediscover {
			replacements[SlotRediscover] = nextRediscover
		} else {
			updatedUnavailable[SlotRediscover] = unavailableFromResult(nextRediscoverResult, defaultUnavailableMessage(SlotRediscover))
		}

	case SlotConnect:
		nextConnectResult, err := service.selectConnect(corpus, currentContinue)
		if err != nil {
			return AnotherResult{}, err
		}
		nextConnect, hasConnect, err := materializeSlotCard(date, nextConnectResult)
		if err != nil {
			return AnotherResult{}, err
		}
		affected = append(affected, materializedSlotResult(nextConnectResult, nextConnect, hasConnect))
		if hasConnect {
			replacements[SlotConnect] = nextConnect
		} else {
			updatedUnavailable[SlotConnect] = unavailableFromResult(nextConnectResult, defaultConnectUnavailableMessage(currentContinue != nil))
		}
	}

	updatedSelection := ensured.selection
	if len(replacements) > 0 || len(removed) > 0 {
		updatedSelection, err = service.persistMutation(date, ensured.selection, replacements, removed)
		if err != nil {
			return AnotherResult{}, err
		}
	}
	activity, err := service.state.RollingActivity(now)
	if err != nil {
		return AnotherResult{}, err
	}
	result := DailyResult{
		Brief:    buildDailyBrief(date, updatedSelection, updatedUnavailable),
		Activity: activity,
	}
	return AnotherResult{
		Daily:    result,
		Affected: affected,
	}, nil
}

// RecordView records a brief-view event for the current local date.
func (service *Service) RecordView() (RollingActivity, error) {
	now, date, err := service.nowAndDate()
	if err != nil {
		return RollingActivity{}, err
	}
	return service.state.RecordVisit(date, now)
}

// RecordOpen records a successful brief card open event.
func (service *Service) RecordOpen(cardID CardID) (RollingActivity, error) {
	now, _, err := service.nowAndDate()
	if err != nil {
		return RollingActivity{}, err
	}
	return service.state.RecordOpen(cardID, now)
}

func (service *Service) ensureSelection(corpus CorpusSnapshot, now time.Time, allowReadWarning bool) (ensuredSelection, error) {
	date := LocalDate(now.Format("2006-01-02"))
	selection, ok, err := service.state.ReadDailySelection(date)
	if err != nil {
		if !allowReadWarning {
			return ensuredSelection{}, err
		}
		composed, unavailable, composeErr := service.composeSelection(date, corpus)
		if composeErr != nil {
			return ensuredSelection{}, composeErr
		}
		return ensuredSelection{
			date:        date,
			selection:   composed,
			unavailable: unavailable,
			warnings:    []ServiceWarning{stateUnreadableWarning(err)},
		}, nil
	}
	if !ok {
		composed, unavailable, err := service.composeSelection(date, corpus)
		if err != nil {
			return ensuredSelection{}, err
		}
		if err := service.state.SaveDailySelection(composed); err != nil {
			if !allowReadWarning {
				return ensuredSelection{}, err
			}
			return ensuredSelection{
				date:        date,
				selection:   composed,
				unavailable: unavailable,
				warnings:    []ServiceWarning{stateUnreadableWarning(err)},
			}, nil
		}
		return ensuredSelection{
			date:      date,
			selection: composed,
		}, nil
	}
	repaired, unavailable, err := service.repairSelection(date, selection, corpus)
	if err != nil {
		return ensuredSelection{}, err
	}
	return ensuredSelection{
		date:        date,
		selection:   repaired,
		unavailable: unavailable,
	}, nil
}

func (service *Service) composeSelection(date LocalDate, corpus CorpusSnapshot) (DailySelection, map[SlotKind]UnavailableSlot, error) {
	unavailable := make(map[SlotKind]UnavailableSlot)
	cards := make([]Card, 0, len(SlotOrder))

	continueResult, err := service.selector.SelectContinue(corpus)
	if err != nil {
		return DailySelection{}, nil, err
	}
	continueCard, hasContinue, err := materializeSlotCard(date, continueResult)
	if err != nil {
		return DailySelection{}, nil, err
	}
	var continuePtr *Card
	if hasContinue {
		cards = append(cards, continueCard)
		cloned := cloneCard(continueCard)
		continuePtr = &cloned
	} else {
		unavailable[SlotContinue] = unavailableFromResult(continueResult, defaultUnavailableMessage(SlotContinue))
	}

	rediscoverResult, err := service.selector.SelectRediscover(corpus, continuePtr)
	if err != nil {
		return DailySelection{}, nil, err
	}
	rediscoverCard, hasRediscover, err := materializeSlotCard(date, rediscoverResult)
	if err != nil {
		return DailySelection{}, nil, err
	}
	if hasRediscover {
		cards = append(cards, rediscoverCard)
	} else {
		unavailable[SlotRediscover] = unavailableFromResult(rediscoverResult, defaultUnavailableMessage(SlotRediscover))
	}

	connectResult, err := service.selectConnect(corpus, continuePtr)
	if err != nil {
		return DailySelection{}, nil, err
	}
	connectCard, hasConnect, err := materializeSlotCard(date, connectResult)
	if err != nil {
		return DailySelection{}, nil, err
	}
	if hasConnect {
		cards = append(cards, connectCard)
	} else {
		unavailable[SlotConnect] = unavailableFromResult(connectResult, defaultConnectUnavailableMessage(continuePtr != nil))
	}

	selection, err := normalizeDailySelection(DailySelection{
		Date:  date,
		Cards: cards,
	})
	if err != nil {
		return DailySelection{}, nil, err
	}
	return selection, unavailable, nil
}
