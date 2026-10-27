// SPDX-License-Identifier: Apache-2.0

package main

import "fmt"

// Plan comparison tables and the plan FAQs. The FAQs deliberately hold pairs of
// questions that differ in one word or number and whose answers differ in one
// detail (Plan 59 vs Plan 59 Plus, Fiber 500 vs Fiber 1000, new vs existing
// customers).

func init() {
	p59, p59p, p99 := nvPlan("Plan 59"), nvPlan("Plan 59 Plus"), nvPlan("Plan 99")
	basic, senior := nvPlan("Basic"), nvPlan("Senior Care")
	f5, f10 := nvPlan("Fiber 500"), nvPlan("Fiber 1000")

	nvPair("plans/mobile-plans-compared",
		fmt.Sprintf(`# Mobile plans compared
Applies to: all mobile customers
## Which mobile plans does NovaNet offer?
NovaNet has five mobile plans: Basic, Plan 59, Plan 59 Plus, Plan 99 and Senior Care. The table shows the monthly fee, data, voice minutes and contract of each.
| Plan | Monthly fee (yuan) | Data per month | Voice minutes | Contract | Network |
| Basic | %s | %d GB | %d | none | 4G |
| Plan 59 | %s | %d GB | %d | %d months | 5G |
| Plan 59 Plus | %s | %d GB | %d | %d months | 5G |
| Plan 99 | %s | %d GB | %d | %d months | 5G |
| Senior Care | %s | %d GB | %d | none | 4G |
## Plan 59 versus Plan 59 Plus?
Both cost %s yuan per month. Plan 59 Plus has %d GB of data and %d voice minutes against %d GB and %d minutes on Plan 59, lets %s, and has a %d-month contract instead of %d months.
## Which plans have no contract?
Basic and Senior Care have no minimum term. Every other mobile plan has a contract.
## Which plan is best for heavy data users?
Plan 99 has the most data, %d GB per month, and lets up to 30 GB of unused data roll over to the next month.
`, yuan(basic.MonthlyFee), basic.DataGB, basic.VoiceMinutes,
			yuan(p59.MonthlyFee), p59.DataGB, p59.VoiceMinutes, p59.ContractMonths,
			yuan(p59p.MonthlyFee), p59p.DataGB, p59p.VoiceMinutes, p59p.ContractMonths,
			yuan(p99.MonthlyFee), p99.DataGB, p99.VoiceMinutes, p99.ContractMonths,
			yuan(senior.MonthlyFee), senior.DataGB, senior.VoiceMinutes,
			yuan(p59.MonthlyFee), p59p.DataGB, p59p.VoiceMinutes, p59.DataGB, p59.VoiceMinutes, "up to 10 GB of unused data roll over", p59p.ContractMonths, p59.ContractMonths,
			p99.DataGB),
		fmt.Sprintf(`# 手机套餐对比
适用对象：全部手机客户
## 星联宽带有哪些手机套餐？
星联宽带有五款手机套餐：基础套餐、59元套餐、59元Plus套餐、99元套餐和敬老套餐。下表列出各套餐的月费、流量、通话分钟数和合约期。
| 套餐 | 月费（元） | 每月流量 | 通话分钟 | 合约期 | 网络 |
| 基础套餐 | %s | %dGB | %d | 无 | 4G |
| 59元套餐 | %s | %dGB | %d | %d个月 | 5G |
| 59元Plus套餐 | %s | %dGB | %d | %d个月 | 5G |
| 99元套餐 | %s | %dGB | %d | %d个月 | 5G |
| 敬老套餐 | %s | %dGB | %d | 无 | 4G |
## 59元套餐和59元Plus套餐有什么区别？
两个套餐月费都是%s元。59元Plus套餐有%dGB流量和%d分钟通话，59元套餐是%dGB和%d分钟；Plus套餐%s，合约期为%d个月，而59元套餐是%d个月。
## 哪些套餐没有合约期？
基础套餐和敬老套餐没有合约期，其他手机套餐都有合约期。
## 流量用得多选哪个套餐？
99元套餐流量最多，每月%dGB，当月剩余流量最多30GB可以结转到下月。
`, yuan(basic.MonthlyFee), basic.DataGB, basic.VoiceMinutes,
			yuan(p59.MonthlyFee), p59.DataGB, p59.VoiceMinutes, p59.ContractMonths,
			yuan(p59p.MonthlyFee), p59p.DataGB, p59p.VoiceMinutes, p59p.ContractMonths,
			yuan(p99.MonthlyFee), p99.DataGB, p99.VoiceMinutes, p99.ContractMonths,
			yuan(senior.MonthlyFee), senior.DataGB, senior.VoiceMinutes,
			yuan(p59.MonthlyFee), p59p.DataGB, p59p.VoiceMinutes, p59.DataGB, p59.VoiceMinutes, "剩余流量最多10GB可以结转", p59p.ContractMonths, p59.ContractMonths,
			p99.DataGB))

	nvPair("plans/broadband-plans-compared",
		fmt.Sprintf(`# Home broadband plans compared
Applies to: home broadband customers
## Which home broadband plans are there?
NovaNet sells two home broadband plans, Fiber 500 and Fiber 1000. Both use fiber to the home.
| Item | Fiber 500 | Fiber 1000 |
| Monthly fee (yuan) | %s | %s |
| Download speed | up to %d Mbps | up to %d Mbps |
| Upload speed | up to 100 Mbps | up to 500 Mbps |
| Router included | R3 | R5 Pro |
| Installation fee | 99 yuan, waived in the app | free |
| TV box | optional, 10 yuan per month | one T2 included |
| Contract | %d months | %d months |
| Early termination fee (yuan) | %s | %s |
## Which plan suits a family of four?
For streaming and video calls at the same time, Fiber 500 is enough for most families. Choose Fiber 1000 for large downloads, many smart-home devices or a home office.
`, yuan(f5.MonthlyFee), yuan(f10.MonthlyFee), f5.SpeedMbps, f10.SpeedMbps, f5.ContractMonths, f10.ContractMonths, yuan(f5.EarlyTermFee), yuan(f10.EarlyTermFee)),
		fmt.Sprintf(`# 家庭宽带套餐对比
适用对象：家庭宽带客户
## 家庭宽带有哪些套餐？
星联宽带提供光纤500和光纤1000两款家庭宽带，均为光纤入户。
| 项目 | 光纤500 | 光纤1000 |
| 月费（元） | %s | %s |
| 下行速率 | 最高%dMbps | 最高%dMbps |
| 上行速率 | 最高100Mbps | 最高500Mbps |
| 含路由器 | R3 | R5 Pro |
| 安装费 | 99元，应用下单免收 | 免费 |
| 电视盒 | 可选，每月10元 | 含一台T2 |
| 合约期 | %d个月 | %d个月 |
| 提前解约费（元） | %s | %s |
## 四口之家选哪款宽带？
同时看视频和视频通话，光纤500对多数家庭已经够用。经常下载大文件、智能家居设备多或在家办公，选光纤1000。
`, yuan(f5.MonthlyFee), yuan(f10.MonthlyFee), f5.SpeedMbps, f10.SpeedMbps, f5.ContractMonths, f10.ContractMonths, yuan(f5.EarlyTermFee), yuan(f10.EarlyTermFee)))

	// Plan FAQ: near-duplicate questions, one detail apart.
	nvPairNamed("faq/plan-faq", "faq/套餐常见问题",
		fmt.Sprintf(`# Plan FAQ
Version: 3.2
Owner: Customer Care
## How much data does Plan 59 include?
Plan 59 includes %d GB of data per month.
## How much data does Plan 59 Plus include?
Plan 59 Plus includes %d GB of data per month.
## How many minutes in Plan 59?
Plan 59 includes %d voice minutes per month.
## How many minutes in Plan 59 Plus?
Plan 59 Plus includes %d voice minutes per month.
## How long is the Plan 59 contract?
The Plan 59 contract is %d months.
## How long is the Plan 59 Plus contract?
The Plan 59 Plus contract is %d months.
## Plan 59 cancellation fee?
Cancelling Plan 59 early costs %s yuan.
## Plan 59 Plus cancellation fee?
Cancelling Plan 59 Plus early costs %s yuan.
## Does Plan 59 data roll over?
No. Unused Plan 59 data is lost at the end of the month.
## Does Plan 59 Plus data roll over?
Yes. Up to 10 GB of unused Plan 59 Plus data rolls over to the next month.
## Which router comes with Fiber 500?
Fiber 500 comes with an R3 router.
## Which router comes with Fiber 1000?
Fiber 1000 comes with an R5 Pro router.
## Is Fiber 500 installation free?
Fiber 500 installation costs 99 yuan, and the fee is waived when you order in the NovaNet app.
## Is Fiber 1000 installation free?
Yes. Fiber 1000 installation is free.
## What is the Fiber 500 upload speed?
Fiber 500 upload speed is up to 100 Mbps.
## What is the Fiber 1000 upload speed?
Fiber 1000 upload speed is up to 500 Mbps.
## Does Plan 99 include free roaming?
Plan 99 includes one free Asia 3 Day roaming pack each calendar year.
## Does Plan 59 Plus include free roaming?
No. Plan 59 Plus gets 10 percent off roaming packs but no free pack.
## Can I use eSIM on Basic?
No. Basic is only issued on a physical SIM card.
## Can I use eSIM on Plan 59?
Yes. Plan 59 can be activated on an eSIM.
`, p59.DataGB, p59p.DataGB, p59.VoiceMinutes, p59p.VoiceMinutes, p59.ContractMonths, p59p.ContractMonths, yuan(p59.EarlyTermFee), yuan(p59p.EarlyTermFee)),
		fmt.Sprintf(`# 套餐常见问题
版本：3.2
负责部门：客户服务部
## 59元套餐有多少流量？
59元套餐每月包含%dGB流量。
## 59元Plus套餐有多少流量？
59元Plus套餐每月包含%dGB流量。
## 59元套餐有多少通话分钟？
59元套餐每月包含%d分钟通话。
## 59元Plus套餐有多少通话分钟？
59元Plus套餐每月包含%d分钟通话。
## 59元套餐合约多久？
59元套餐的合约期是%d个月。
## 59元Plus套餐合约多久？
59元Plus套餐的合约期是%d个月。
## 59元套餐提前取消要多少钱？
59元套餐提前取消需支付%s元。
## 59元Plus套餐提前取消要多少钱？
59元Plus套餐提前取消需支付%s元。
## 59元套餐流量能结转吗？
不能。59元套餐当月剩余流量月底清零。
## 59元Plus套餐流量能结转吗？
能。59元Plus套餐当月剩余流量最多10GB可结转到下月。
## 光纤500送什么路由器？
光纤500赠送R3路由器。
## 光纤1000送什么路由器？
光纤1000赠送R5 Pro路由器。
## 光纤500安装免费吗？
光纤500安装费99元，通过星联宽带应用下单可免收。
## 光纤1000安装免费吗？
免费。光纤1000不收安装费。
## 光纤500上行速率是多少？
光纤500上行速率最高100Mbps。
## 光纤1000上行速率是多少？
光纤1000上行速率最高500Mbps。
## 99元套餐含免费漫游吗？
99元套餐每个自然年赠送一个亚洲3日漫游包。
## 59元Plus套餐含免费漫游吗？
不含免费漫游包，但购买漫游包可享九折。
## 基础套餐能用eSIM吗？
不能。基础套餐只提供实体SIM卡。
## 59元套餐能用eSIM吗？
能。59元套餐可以开通eSIM。
`, p59.DataGB, p59p.DataGB, p59.VoiceMinutes, p59p.VoiceMinutes, p59.ContractMonths, p59p.ContractMonths, yuan(p59.EarlyTermFee), yuan(p59p.EarlyTermFee)))

	// New versus existing customers.
	nvPairNamed("faq/new-vs-existing-customers", "faq/新老客户权益问答",
		`# New and existing customers
Applies to: all customers
## Welcome offer for new customers?
New customers get 20 GB of bonus data in their first month on any mobile plan, and the first month of a home broadband plan is free of the monthly fee.
## Welcome offer for existing customers?
No. The welcome offer is for new customers only. Existing customers can buy a Booster data pack instead.
## Installation time for new customers?
A new broadband customer is usually connected within 3 business days after the order is confirmed. Some districts differ, so check the notice for your district.
## Upgrade time for existing customers?
An existing Fiber 500 customer who upgrades to Fiber 1000 needs a technician visit to fit a G2 optical modem. It takes up to 5 business days from the request.
## Can a new customer keep a number?
Yes. A new customer can keep their number by porting it in during sign-up. Porting takes 1 to 3 business days.
## Can an existing customer change number?
Yes. An existing customer can choose a new number at a service hall. The change costs 30 yuan.
`,
		`# 新老客户权益问答
适用对象：全部客户
## 新客户有什么入网优惠？
新客户办理任意手机套餐，首月赠送20GB流量；办理家庭宽带，首月免收月费。
## 老客户能享受入网优惠吗？
不能。入网优惠仅限新客户，老客户可以另外购买加油包流量。
## 新客户办宽带多久能装好？
新客户宽带订单确认后，一般3个工作日内接通。个别区域不同，请以所在区域的公告为准。
## 老客户升级网速要多久？
光纤500老客户升级到光纤1000，需要师傅上门更换G2光猫，自申请起最长5个工作日。
## 新客户可以保留原来的号码吗？
可以。新客户办理时可以办理携号转入，携号转入需要1到3个工作日。
## 老客户可以换号码吗？
可以。老客户可以到营业厅选新号码，换号费用30元。
`)
}
