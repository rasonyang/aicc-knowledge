// SPDX-License-Identifier: Apache-2.0

package main

import (
	"fmt"
	"strings"
)

// Plan sheets: one document per plan in each language, written from plans.go
// so the figures agree with the plans fact table.

func nvPlan(key string) plan {
	for _, p := range plans {
		if p.Key == key {
			return p
		}
	}
	panic("unknown plan " + key)
}

// yuan renders a decimal string without trailing zeros: "59.00" is "59".
func yuan(s string) string {
	if strings.HasSuffix(s, ".00") {
		return strings.TrimSuffix(s, ".00")
	}
	return s
}

// planX holds what the plan sheets say beyond plans.go.
type planX struct {
	slug                 string
	hotspotEN, hotspotZH string
	rollEN, rollZH       string
	roamEN, roamZH       string
	eligEN, eligZH       string
	net                  string
	esim                 bool
	extraEN, extraZH     string
	// broadband
	upMbps               int
	router               string
	installEN, installZH string
	tvEN, tvZH           string
}

var planExtras = map[string]planX{
	"Basic": {
		slug:      "basic-plan",
		hotspotEN: "Hotspot sharing is not available on Basic.", hotspotZH: "基础套餐不支持开启热点共享。",
		rollEN: "Unused data does not roll over.", rollZH: "当月剩余流量不能结转到下月。",
		roamEN: "Basic does not include roaming. You can buy a roaming pack in the NovaNet app before you travel.", roamZH: "基础套餐不含漫游。出行前可以在星联宽带应用里购买漫游包。",
		eligEN: "Any customer aged 18 or older with a valid ID can sign up for Basic.", eligZH: "年满18周岁、持有效证件的客户都可以办理基础套餐。",
		net: "4G", esim: false,
		extraEN: "Basic is a 4G plan. It has no minimum term, so you can leave at any time.", extraZH: "基础套餐为4G套餐，没有合约期，随时可以取消。",
	},
	"Plan 59": {
		slug:      "plan-59",
		hotspotEN: "You can share your connection as a hotspot, up to 10 GB per month. Hotspot use above 10 GB is slowed down.", hotspotZH: "可以开启热点共享，每月最多10GB，超出后热点网速会被限制。",
		rollEN: "Unused data does not roll over to the next month.", rollZH: "当月剩余流量不能结转到下月。",
		roamEN: "Plan 59 does not include roaming. Roaming packs can be added in the NovaNet app.", roamZH: "59元套餐不含漫游，可以在星联宽带应用里另外购买漫游包。",
		eligEN: "Any customer aged 18 or older with a valid ID can sign up for Plan 59.", eligZH: "年满18周岁、持有效证件的客户都可以办理59元套餐。",
		net: "5G", esim: true,
		extraEN: "Plan 59 runs on the 5G network where there is coverage.", extraZH: "59元套餐在有覆盖的地方使用5G网络。",
	},
	"Plan 59 Plus": {
		slug:      "plan-59-plus",
		hotspotEN: "Hotspot sharing is allowed and counts against your 60 GB. There is no separate hotspot cap.", hotspotZH: "可以开启热点共享，流量计入每月60GB，没有单独的热点上限。",
		rollEN: "Up to 10 GB of unused data rolls over to the next month. Rolled-over data is used first and expires at the end of that month.", rollZH: "当月剩余流量最多10GB可以结转到下月，结转流量优先使用，下月底清零。",
		roamEN: "Plan 59 Plus customers get 10 percent off every roaming pack.", roamZH: "59元Plus套餐用户购买任意漫游包可享九折优惠。",
		eligEN: "Any customer aged 18 or older with a valid ID can sign up for Plan 59 Plus.", eligZH: "年满18周岁、持有效证件的客户都可以办理59元Plus套餐。",
		net: "5G", esim: true,
		extraEN: "Plan 59 Plus costs the same each month as Plan 59, but it has more data and voice minutes and a longer contract.", extraZH: "59元Plus套餐月费与59元套餐相同，但流量和通话分钟数更多，合约期也更长。",
	},
	"Plan 99": {
		slug:      "plan-99",
		hotspotEN: "Hotspot sharing is allowed and counts against your 100 GB.", hotspotZH: "可以开启热点共享，流量计入每月100GB。",
		rollEN: "Up to 30 GB of unused data rolls over to the next month.", rollZH: "当月剩余流量最多30GB可以结转到下月。",
		roamEN: "Plan 99 includes one free Asia 3 Day roaming pack each calendar year. Other packs are sold at the normal price.", roamZH: "99元套餐每个自然年赠送一个亚洲3日漫游包，其他漫游包按正常价格购买。",
		eligEN: "Any customer aged 18 or older with a valid ID can sign up for Plan 99.", eligZH: "年满18周岁、持有效证件的客户都可以办理99元套餐。",
		net: "5G", esim: true,
		extraEN: "Plan 99 also includes two extra SIM cards for family members at no charge, and they share the same data.", extraZH: "99元套餐另赠两张家庭副卡，免费使用，与主卡共享流量。",
	},
	"Senior Care": {
		slug:      "senior-care",
		hotspotEN: "Hotspot sharing is not available on Senior Care.", hotspotZH: "敬老套餐不支持开启热点共享。",
		rollEN: "Unused data does not roll over.", rollZH: "当月剩余流量不能结转到下月。",
		roamEN: "Senior Care does not include roaming.", roamZH: "敬老套餐不含漫游。",
		eligEN: "Senior Care is for customers aged 65 or older. We check the ID at sign-up, in a service hall or in the NovaNet app.", eligZH: "敬老套餐面向年满65周岁的客户，办理时需核验身份证件，可到营业厅或在星联宽带应用里办理。",
		net: "4G", esim: false,
		extraEN: "Senior Care bills arrive as large-print text messages, and calls to customer care from a Senior Care number are answered first.", extraZH: "敬老套餐的账单以大字短信发送，敬老套餐号码致电客服时优先接入。",
	},
	"Fiber 500": {
		slug:      "fiber-500",
		upMbps:    100,
		router:    "R3",
		installEN: "Installation costs 99 yuan. The fee is waived when you order in the NovaNet app.", installZH: "安装费99元，通过星联宽带应用下单可免安装费。",
		tvEN: "A T2 TV box can be added for 10 yuan per month.", tvZH: "可另加电视盒T2，每月10元。",
		eligEN: "Fiber 500 is available at addresses inside a NovaNet service area.", eligZH: "光纤500适用于星联宽带服务区域内的地址。",
	},
	"Fiber 1000": {
		slug:      "fiber-1000",
		upMbps:    500,
		router:    "R5 Pro",
		installEN: "Installation is free.", installZH: "免收安装费。",
		tvEN: "One T2 TV box is included at no charge.", tvZH: "含一台电视盒T2，免费使用。",
		eligEN: "Fiber 1000 is available at addresses inside a NovaNet service area that has been upgraded to 10G fiber. Your home also needs a G2 optical modem, which we supply.", eligZH: "光纤1000适用于星联宽带已升级万兆光网的服务区域内的地址，家里还需要G2光猫，由我们提供。",
	},
}

func mobileSheetEN(p plan, x planX, questions bool) string {
	h := func(q, t string) string {
		if questions {
			return q
		}
		return t
	}
	contract := fmt.Sprintf("%s has a %d-month contract. If you cancel before the contract ends, an early termination fee applies.", p.NameEN, p.ContractMonths)
	etf := fmt.Sprintf("If you cancel %s before the %d-month contract ends, the early termination fee is %s yuan.", p.NameEN, p.ContractMonths, yuan(p.EarlyTermFee))
	if p.ContractMonths == 0 {
		contract = fmt.Sprintf("%s has no minimum term. You can cancel at any time.", p.NameEN)
		etf = fmt.Sprintf("%s has no contract, so there is no early termination fee.", p.NameEN)
	}
	esim := fmt.Sprintf("Yes. %s can be activated on an eSIM as well as on a physical SIM.", p.NameEN)
	if !x.esim {
		esim = fmt.Sprintf("No. %s is only issued on a physical SIM card.", p.NameEN)
	}
	return fmt.Sprintf(`# %[1]s plan sheet
Applies to: mobile customers
Owner: Product Management
## %[2]s
%[1]s costs %[3]s yuan per month and includes %[4]d GB of data and %[5]d voice minutes. %[6]s
| Item | %[1]s |
| Monthly fee | %[3]s yuan |
| Data per month | %[4]d GB |
| Voice minutes per month | %[5]d |
| Network | %[7]s |
| Contract | %[8]d months |
## %[9]s
%[10]s
## %[11]s
%[12]s
## %[13]s
%[14]s
## %[15]s
%[16]s
## %[17]s
%[18]s
## %[19]s
%[20]s
## %[21]s
%[22]s
`, p.NameEN, h("What does "+p.NameEN+" include?", "Included allowances"), yuan(p.MonthlyFee), p.DataGB, p.VoiceMinutes, x.extraEN, x.net, p.ContractMonths,
		h("How long is the "+p.NameEN+" contract?", "Contract term"), contract,
		h("Early termination fee for "+p.NameEN+"?", "Early termination fee"), etf,
		h("Can I use "+p.NameEN+" as a hotspot?", "Hotspot sharing"), x.hotspotEN,
		h("Does unused data roll over?", "Data rollover"), x.rollEN,
		h("Is roaming included?", "Roaming"), x.roamEN,
		h("Who can sign up for "+p.NameEN+"?", "Eligibility"), x.eligEN,
		h("Does "+p.NameEN+" work with eSIM?", "eSIM support"), esim)
}

func mobileSheetZH(p plan, x planX, questions bool) string {
	h := func(q, t string) string {
		if questions {
			return q
		}
		return t
	}
	contract := fmt.Sprintf("%s的合约期为%d个月，合约期内取消需要支付提前解约费。", p.NameZH, p.ContractMonths)
	etf := fmt.Sprintf("%s在%d个月合约期满前取消，需支付提前解约费%s元。", p.NameZH, p.ContractMonths, yuan(p.EarlyTermFee))
	if p.ContractMonths == 0 {
		contract = fmt.Sprintf("%s没有合约期，可以随时取消。", p.NameZH)
		etf = fmt.Sprintf("%s没有合约期，取消时不收提前解约费。", p.NameZH)
	}
	esim := fmt.Sprintf("可以。%s既可以用实体SIM卡，也可以开通eSIM。", p.NameZH)
	if !x.esim {
		esim = fmt.Sprintf("不可以。%s只提供实体SIM卡。", p.NameZH)
	}
	return fmt.Sprintf(`# %[1]s资费说明
适用对象：手机客户
负责部门：产品管理部
## %[2]s
%[1]s每月%[3]s元，包含%[4]dGB流量和%[5]d分钟通话。%[6]s
| 项目 | %[1]s |
| 月费 | %[3]s元 |
| 每月流量 | %[4]dGB |
| 每月通话 | %[5]d分钟 |
| 网络 | %[7]s |
| 合约期 | %[8]d个月 |
## %[9]s
%[10]s
## %[11]s
%[12]s
## %[13]s
%[14]s
## %[15]s
%[16]s
## %[17]s
%[18]s
## %[19]s
%[20]s
## %[21]s
%[22]s
`, p.NameZH, h(p.NameZH+"包含哪些权益？", "套餐包含内容"), yuan(p.MonthlyFee), p.DataGB, p.VoiceMinutes, x.extraZH, x.net, p.ContractMonths,
		h(p.NameZH+"的合约期是多久？", "合约期"), contract,
		h(p.NameZH+"提前解约要交多少钱？", "提前解约费"), etf,
		h(p.NameZH+"能开热点吗？", "热点共享"), x.hotspotZH,
		h("剩余流量能结转吗？", "流量结转"), x.rollZH,
		h("套餐包含漫游吗？", "漫游"), x.roamZH,
		h("谁可以办理"+p.NameZH+"？", "办理条件"), x.eligZH,
		h(p.NameZH+"支持eSIM吗？", "eSIM支持"), esim)
}

func fiberSheetEN(p plan, x planX, questions bool) string {
	h := func(q, t string) string {
		if questions {
			return q
		}
		return t
	}
	return fmt.Sprintf(`# %[1]s home broadband
Applies to: home broadband customers
Owner: Product Management
## %[2]s
%[1]s costs %[3]s yuan per month. Download speed is up to %[4]d Mbps and upload speed is up to %[5]d Mbps. A %[6]s router is included.
| Item | %[1]s |
| Monthly fee | %[3]s yuan |
| Download speed | up to %[4]d Mbps |
| Upload speed | up to %[5]d Mbps |
| Router included | %[6]s |
| Contract | %[7]d months |
## %[8]s
%[1]s has a %[7]d-month contract. If you cancel earlier, the early termination fee is %[9]s yuan.
## %[10]s
%[11]s
## %[12]s
%[13]s
## %[14]s
The %[6]s router is included with %[1]s. You keep using it as long as the plan is active and return it when you cancel.
## %[15]s
%[16]s
`, p.NameEN, h("What does "+p.NameEN+" include?", "Included speed and equipment"), yuan(p.MonthlyFee), p.SpeedMbps, x.upMbps, x.router, p.ContractMonths,
		h(p.NameEN+" contract and fee?", "Contract and early termination"), yuan(p.EarlyTermFee),
		h("How much is installation for "+p.NameEN+"?", "Installation fee"), x.installEN,
		h("Does "+p.NameEN+" include a TV box?", "TV box"), x.tvEN,
		h("Which router comes with "+p.NameEN+"?", "Router"),
		h("Who can order "+p.NameEN+"?", "Availability"), x.eligEN)
}

func fiberSheetZH(p plan, x planX, questions bool) string {
	h := func(q, t string) string {
		if questions {
			return q
		}
		return t
	}
	return fmt.Sprintf(`# %[1]s家庭宽带资费说明
适用对象：家庭宽带客户
负责部门：产品管理部
## %[2]s
%[1]s每月%[3]s元，下行速率最高%[4]dMbps，上行速率最高%[5]dMbps，含%[6]s路由器一台。
| 项目 | %[1]s |
| 月费 | %[3]s元 |
| 下行速率 | 最高%[4]dMbps |
| 上行速率 | 最高%[5]dMbps |
| 含路由器 | %[6]s |
| 合约期 | %[7]d个月 |
## %[8]s
%[1]s的合约期为%[7]d个月，提前取消需支付提前解约费%[9]s元。
## %[10]s
%[11]s
## %[12]s
%[13]s
## %[14]s
办理%[1]s赠送%[6]s路由器，套餐有效期间可继续使用，取消时需归还。
## %[15]s
%[16]s
`, p.NameZH, h(p.NameZH+"包含哪些速率和设备？", "含速率与设备"), yuan(p.MonthlyFee), p.SpeedMbps, x.upMbps, x.router, p.ContractMonths,
		h(p.NameZH+"合约期和提前解约费是多少？", "合约期与提前解约"), yuan(p.EarlyTermFee),
		h(p.NameZH+"安装费多少？", "安装费"), x.installZH,
		h(p.NameZH+"含电视盒吗？", "电视盒"), x.tvZH,
		h(p.NameZH+"送哪款路由器？", "路由器"),
		h("谁可以办理"+p.NameZH+"？", "办理范围"), x.eligZH)
}

func init() {
	i := 0
	for _, p := range plans {
		x := planExtras[p.Key]
		i++
		q := i%2 == 0
		var en, zh string
		if p.Kind == "BROADBAND" {
			en, zh = fiberSheetEN(p, x, q), fiberSheetZH(p, x, q)
			nvPairNamed("plans/"+x.slug+"-sheet", "plans/"+p.NameZH+"家庭宽带资费说明", en, zh)
		} else {
			en, zh = mobileSheetEN(p, x, q), mobileSheetZH(p, x, q)
			nvPairNamed("plans/"+x.slug+"-sheet", "plans/"+p.NameZH+"资费说明", en, zh)
		}
	}
}
