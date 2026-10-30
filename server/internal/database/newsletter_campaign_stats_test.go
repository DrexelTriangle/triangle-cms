package database

import (
	"reflect"
	"testing"

	"server/internal/models"
)

// Send errors are shown to every editor, and a mail server's refusal often
// quotes the address it refused.
func TestMaskSendError_HidesAddresses(t *testing.T) {
	for in, want := range map[string]string{
		"550 5.1.1 <jane.doe+news@example.org>: Recipient address rejected": "550 5.1.1 <[address]>: Recipient address rejected",
		"gmail: 400 Invalid To header":                                      "gmail: 400 Invalid To header",
		"to=bob@drexel.edu, cc=Alice <alice@x.co>":                          "to=[address], cc=Alice <[address]>",
		"": "",
	} {
		if got := maskSendError(in); got != want {
			t.Errorf("maskSendError(%q) = %q, want %q", in, got, want)
		}
	}
}

// Two refusals that differ only in the address are the same error.
func TestGroupSendErrors_GroupsAfterMasking(t *testing.T) {
	got := groupSendErrors([]sendErrorCount{
		{"550 <a@example.org>: unknown user", 1},
		{"gmail: 400 Invalid To header", 2},
		{"550 <b@example.org>: unknown user", 2},
		{"", 1},
	})
	want := []models.NewsletterSendError{
		{Message: "550 <[address]>: unknown user", Count: 3},
		{Message: "gmail: 400 Invalid To header", Count: 2},
		{Message: "Unknown error", Count: 1},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("groupSendErrors = %+v, want %+v", got, want)
	}
}

func TestCampaignRates_NullWithoutADenominator(t *testing.T) {
	s := models.NewsletterCampaignSummary{
		Recipients:     models.NewsletterCampaignRecipients{Sent: 200},
		UniqueOpens:    90,
		UniqueClickers: 30,
	}
	fillCampaignRates(&s)
	if s.OpenRate == nil || *s.OpenRate != 0.45 || s.ClickRate == nil || *s.ClickRate != 0.15 ||
		s.ClickToOpenRate == nil || *s.ClickToOpenRate != 30.0/90.0 {
		t.Fatalf("rates: %v %v %v", s.OpenRate, s.ClickRate, s.ClickToOpenRate)
	}

	empty := models.NewsletterCampaignSummary{}
	fillCampaignRates(&empty)
	if empty.OpenRate != nil || empty.ClickRate != nil || empty.ClickToOpenRate != nil || empty.UnsubscribeRate != nil {
		t.Fatalf("a campaign with nothing sent has no rates, got %+v", empty)
	}

	unsubscribes := 4
	s.Unsubscribes = &unsubscribes
	fillCampaignRates(&s)
	if s.UnsubscribeRate == nil || *s.UnsubscribeRate != 0.02 {
		t.Fatalf("unsubscribe rate: %v", s.UnsubscribeRate)
	}
}
