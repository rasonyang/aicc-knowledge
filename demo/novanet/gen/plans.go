// SPDX-License-Identifier: Apache-2.0

package main

// plan is one row of the plans fact table. The documents that describe a plan
// read these values too, so the fact table and the FAQ never disagree on the
// figures they both state.
type plan struct {
	Key            string // fact table key, exactly as stored and looked up
	NameEN, NameZH string
	Kind           string // MOBILE or BROADBAND
	MonthlyFee     string // CNY; written to the sheet as text, the DECIMAL type parses it
	DataGB         int    // MOBILE only
	VoiceMinutes   int    // MOBILE only
	SpeedMbps      int    // BROADBAND only
	ContractMonths int    // 0 means no minimum term
	EarlyTermFee   string // CNY
	// OldMonthlyFee, when set, is the fee that applied until yesterday: the
	// sheet then has two rows for the plan, the old one ending where the new
	// one starts (today).
	OldMonthlyFee string
}

// plans lists every plan of the demo carrier. Senior Care is the row a
// caller's enum deliberately leaves out.
var plans = []plan{
	{Key: "Basic", NameEN: "Basic", NameZH: "基础套餐", Kind: "MOBILE", MonthlyFee: "29.00", DataGB: 5, VoiceMinutes: 100, EarlyTermFee: "0.00"},
	{Key: "Plan 59", NameEN: "Plan 59", NameZH: "59元套餐", Kind: "MOBILE", MonthlyFee: "59.00", DataGB: 30, VoiceMinutes: 300, ContractMonths: 12, EarlyTermFee: "100.00"},
	{Key: "Plan 59 Plus", NameEN: "Plan 59 Plus", NameZH: "59元Plus套餐", Kind: "MOBILE", MonthlyFee: "59.00", DataGB: 60, VoiceMinutes: 500, ContractMonths: 24, EarlyTermFee: "200.00"},
	{Key: "Plan 99", NameEN: "Plan 99", NameZH: "99元套餐", Kind: "MOBILE", MonthlyFee: "99.00", DataGB: 100, VoiceMinutes: 1000, ContractMonths: 24, EarlyTermFee: "300.00"},
	{Key: "Fiber 500", NameEN: "Fiber 500", NameZH: "光纤500", Kind: "BROADBAND", MonthlyFee: "129.00", OldMonthlyFee: "139.00", SpeedMbps: 500, ContractMonths: 12, EarlyTermFee: "200.00"},
	{Key: "Fiber 1000", NameEN: "Fiber 1000", NameZH: "光纤1000", Kind: "BROADBAND", MonthlyFee: "199.00", SpeedMbps: 1000, ContractMonths: 24, EarlyTermFee: "400.00"},
	{Key: "Senior Care", NameEN: "Senior Care", NameZH: "敬老套餐", Kind: "MOBILE", MonthlyFee: "19.00", DataGB: 3, VoiceMinutes: 300, EarlyTermFee: "0.00"},
}
