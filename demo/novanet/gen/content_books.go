// SPDX-License-Identifier: Apache-2.0

package main

import (
	"fmt"

	"github.com/rasonyang/aicc-knowledge/internal/xlsx"
)

// Workbooks: reference tables whose sheets are read as content, and the two
// curated Q&A workbooks (with their .qa.yaml mappings).

func init() {
	basic, p59, p59p, p99, senior := nvPlan("Basic"), nvPlan("Plan 59"), nvPlan("Plan 59 Plus"), nvPlan("Plan 99"), nvPlan("Senior Care")
	f5, f10 := nvPlan("Fiber 500"), nvPlan("Fiber 1000")
	months := func(n int, zh bool) string {
		switch {
		case n == 0 && zh:
			return "无合约"
		case n == 0:
			return "none"
		case zh:
			return fmt.Sprintf("%d个月", n)
		}
		return fmt.Sprintf("%d months", n)
	}

	// Tariff sheet.
	mobEN := [][]string{{"Plan", "Monthly fee (yuan)", "Data (GB)", "Voice minutes", "Contract", "Early termination fee (yuan)", "Hotspot", "eSIM"}}
	mobZH := [][]string{{"套餐", "月费（元）", "流量（GB）", "通话分钟", "合约期", "提前解约费（元）", "热点", "eSIM"}}
	hot := map[string][2]string{"Basic": {"no", "否"}, "Plan 59": {"up to 10 GB", "最多10GB"}, "Plan 59 Plus": {"yes", "是"}, "Plan 99": {"yes", "是"}, "Senior Care": {"no", "否"}}
	esim := map[string][2]string{"Basic": {"no", "否"}, "Plan 59": {"yes", "是"}, "Plan 59 Plus": {"yes", "是"}, "Plan 99": {"yes", "是"}, "Senior Care": {"no", "否"}}
	for _, p := range []plan{basic, p59, p59p, p99, senior} {
		mobEN = append(mobEN, []string{p.NameEN, yuan(p.MonthlyFee), fmt.Sprint(p.DataGB), fmt.Sprint(p.VoiceMinutes), months(p.ContractMonths, false), yuan(p.EarlyTermFee), hot[p.Key][0], esim[p.Key][0]})
		mobZH = append(mobZH, []string{p.NameZH, yuan(p.MonthlyFee), fmt.Sprint(p.DataGB), fmt.Sprint(p.VoiceMinutes), months(p.ContractMonths, true), yuan(p.EarlyTermFee), hot[p.Key][1], esim[p.Key][1]})
	}
	bbEN := [][]string{{"Plan", "Monthly fee (yuan)", "Download (Mbps)", "Upload (Mbps)", "Router", "Contract", "Early termination fee (yuan)", "Installation fee (yuan)"}}
	bbZH := [][]string{{"套餐", "月费（元）", "下行（Mbps）", "上行（Mbps）", "路由器", "合约期", "提前解约费（元）", "安装费（元）"}}
	for _, p := range []plan{f5, f10} {
		x := planExtras[p.Key]
		inst, instZ := "99, waived in the app", "99，应用下单免收"
		if p.Key == "Fiber 1000" {
			inst, instZ = "0", "0"
		}
		bbEN = append(bbEN, []string{p.NameEN, yuan(p.MonthlyFee), fmt.Sprint(p.SpeedMbps), fmt.Sprint(x.upMbps), x.router, months(p.ContractMonths, false), yuan(p.EarlyTermFee), inst})
		bbZH = append(bbZH, []string{p.NameZH, yuan(p.MonthlyFee), fmt.Sprint(p.SpeedMbps), fmt.Sprint(x.upMbps), x.router, months(p.ContractMonths, true), yuan(p.EarlyTermFee), instZ})
	}
	nvAddBook(nvBook{key: "en/tariffs/tariff-sheet-2026.xlsx", sheets: []nvSheet{{"Mobile plans", mobEN}, {"Broadband plans", bbEN}}})
	nvAddBook(nvBook{key: "zh/tariffs/资费总表_2026.xlsx", sheets: []nvSheet{{"手机套餐", mobZH}, {"宽带套餐", bbZH}}})

	// One-off fee schedule.
	feesEN := [][]string{{"Item", "Fee (yuan)", "Note"},
		{"Replacement SIM", "20", "Free within 12 months of activation if the SIM stopped working"},
		{"Number change", "30", "Old number held for 30 days"},
		{"Paused line", "5", "Per month, up to 90 days a year"},
		{"Repair visit for a fault in your own equipment", "80", "Free when the fault is on the NovaNet side"},
		{"Lost R3 router", "300", "Charged at replacement price"},
		{"Lost R5 Pro router", "600", "Charged at replacement price"},
		{"Extra SIM on Plan 59 Plus", "10", "Per month"},
		{"Fiber 500 installation", "99", "Waived when ordered in the app"},
		{"Upgrade Fiber 500 to Fiber 1000", "50", "One-off upgrade fee"},
		{"Excess voice minutes", "0.20", "Per minute beyond the plan allowance"},
		{"Third relocation of broadband", "100", "Two relocations are free during the contract"},
	}
	feesZH := [][]string{{"项目", "费用（元）", "备注"},
		{"补办SIM卡", "20", "开通后12个月内SIM卡自行失效的免费补办"},
		{"更换号码", "30", "旧号码保留30天"},
		{"号码暂停", "5", "每月收费，每年最多90天"},
		{"自有设备故障上门维修", "80", "故障在星联宽带一侧的免费"},
		{"R3路由器丢失", "300", "按更换价收费"},
		{"R5 Pro路由器丢失", "600", "按更换价收费"},
		{"59元Plus套餐加办副卡", "10", "每月"},
		{"光纤500安装费", "99", "应用下单免收"},
		{"光纤500升级光纤1000", "50", "一次性升级费"},
		{"超出套餐的通话", "0.20", "每分钟"},
		{"宽带第三次移机", "100", "合约期内免费移机两次"},
	}
	nvAddBook(nvBook{key: "en/billing/fee-schedule.xlsx", sheets: []nvSheet{{"One-off fees", feesEN}}})
	nvAddBook(nvBook{key: "zh/billing/收费项目表.xlsx", sheets: []nvSheet{{"一次性费用", feesZH}}})

	// International call rates from a NovaNet line at home.
	rateEN := [][]string{{"Region", "Rate per minute (yuan)", "Rate for texts (yuan)"},
		{"Asia-Pacific", "1.50", "0.50"}, {"Europe", "2.00", "0.50"}, {"Americas", "2.50", "0.50"}, {"Africa", "3.00", "0.80"}, {"Middle East", "3.00", "0.80"}}
	rateZH := [][]string{{"地区", "每分钟费率（元）", "短信费率（元）"},
		{"亚太", "1.50", "0.50"}, {"欧洲", "2.00", "0.50"}, {"美洲", "2.50", "0.50"}, {"非洲", "3.00", "0.80"}, {"中东", "3.00", "0.80"}}
	nvAddBook(nvBook{key: "en/roaming/international-call-rates.xlsx", sheets: []nvSheet{{"Call rates", rateEN}}})
	nvAddBook(nvBook{key: "zh/roaming/国际长途费率.xlsx", sheets: []nvSheet{{"长途费率", rateZH}}})

	// Coverage and service hall lists, from the same data as the district pages.
	covEN := [][]string{{"District", "Fiber 500", "Fiber 1000", "Installation lead time (business days)", "Service hall"}}
	covZH := [][]string{{"区域", "光纤500", "光纤1000", "安装时效（工作日）", "营业厅"}}
	hallEN := [][]string{{"Service hall", "Opening hours", "Location"}}
	hallZH := [][]string{{"营业厅", "营业时间", "位置"}}
	yn := func(b bool) (string, string) {
		if b {
			return "yes", "有"
		}
		return "no", "无"
	}
	avail := func(d district, tenG bool) (string, string) {
		ok := d.status == "OPEN" && (!tenG || d.g10)
		if ok {
			return "available", "可办"
		}
		if d.status == "SOON" {
			return "from " + d.soonEN, d.soonZH + "起"
		}
		return "not available", "暂不可办"
	}
	hi := 0
	for _, d := range districts {
		a5e, a5z := avail(d, false)
		a10e, a10z := avail(d, true)
		he, hz := yn(d.hall)
		lead := fmt.Sprint(d.lead)
		if d.status != "OPEN" {
			lead = "-"
		}
		covEN = append(covEN, []string{d.en, a5e, a10e, lead, he})
		covZH = append(covZH, []string{d.zh, a5z, a10z, lead, hz})
		if d.hall {
			hi++
			he, hz := hallHours(hi)
			hallEN = append(hallEN, []string{d.en + " hall", he, d.plaza})
			hallZH = append(hallZH, []string{d.zh + "营业厅", hz, d.plazaZ})
		}
	}
	nvAddBook(nvBook{key: "en/network/coverage-list.xlsx", sheets: []nvSheet{{"Coverage", covEN}}})
	nvAddBook(nvBook{key: "zh/network/覆盖区域一览.xlsx", sheets: []nvSheet{{"覆盖区域", covZH}}})
	nvAddBook(nvBook{key: "en/stores/service-hall-list.xlsx", sheets: []nvSheet{{"Halls", hallEN}}})
	nvAddBook(nvBook{key: "zh/stores/营业厅一览.xlsx", sheets: []nvSheet{{"营业厅", hallZH}}})

	// Device compatibility.
	devEN := [][]string{{"Device", "Works with", "Does not work with", "Max connected devices"},
		{"R3", "Fiber 500 (included)", "M1 mesh extender", "64"},
		{"R5", "Fiber 500 and Fiber 1000, M1 mesh extender (up to 2)", "-", "128"},
		{"R5 Pro", "Fiber 1000 (included), M1 mesh extender (up to 4)", "-", "256"},
		{"G1 optical modem", "Fiber 500", "Fiber 1000", "-"},
		{"G2 optical modem", "Fiber 500 and Fiber 1000", "-", "-"},
		{"T2 TV box", "all broadband plans", "-", "-"},
		{"T2 Max TV box", "all broadband plans", "-", "-"},
		{"M1 mesh extender", "R5 and R5 Pro", "R3", "-"}}
	devZH := [][]string{{"设备", "适用", "不适用", "最多连接设备数"},
		{"R3", "光纤500（赠送）", "M1扩展器", "64"},
		{"R5", "光纤500和光纤1000，M1扩展器（最多2个）", "-", "128"},
		{"R5 Pro", "光纤1000（赠送），M1扩展器（最多4个）", "-", "256"},
		{"G1光猫", "光纤500", "光纤1000", "-"},
		{"G2光猫", "光纤500和光纤1000", "-", "-"},
		{"T2电视盒", "全部宽带套餐", "-", "-"},
		{"T2 Max电视盒", "全部宽带套餐", "-", "-"},
		{"M1扩展器", "R5和R5 Pro", "R3", "-"}}
	nvAddBook(nvBook{key: "en/devices/device-compatibility.xlsx", sheets: []nvSheet{{"Compatibility", devEN}}})
	nvAddBook(nvBook{key: "zh/devices/设备适配表.xlsx", sheets: []nvSheet{{"适配表", devZH}}})
}

func qaMapping(sheet, q, a, alt, lang string) *xlsx.QAMappingFile {
	return &xlsx.QAMappingFile{Sheets: []xlsx.QASheet{{Sheet: sheet, HeaderStartRow: 2, HeaderRows: 1, Question: q, Answer: a, Alternates: []string{alt}, Language: lang}}}
}

func init() {
	// English quick answers. The cooling-off row is stale on purpose: it still
	// states the 7 days of the replaced policy.
	en := [][]string{
		{"NovaNet customer care: quick answers"},
		{"Question", "Answer", "Other ways to ask"},
		{"How do I contact customer care?", "Chat with us in the NovaNet app at any time, or call customer care every day from 8:00 to 22:00.", "how can I talk to someone\nwhat are your support hours\nis there a live chat"},
		{"Can I change my plan online?", "Yes. Open My plan in the NovaNet app and choose the new plan. An upgrade starts at once and a downgrade starts on your next bill date.", "how do I switch plans\nI want to upgrade my plan\ncan I move to a cheaper plan"},
		{"How do I check my data usage?", "Open Usage in the NovaNet app to see the data, minutes and roaming you have used since your bill date.", "how much data have I used\nwhere can I see my remaining data"},
		{"Is Plan 59 Plus cheaper than Plan 59?", "No. Both cost 59 yuan a month. Plan 59 Plus has more data and more minutes, but it has a longer contract.", "plan fifty nine plus price\nwhat is the difference in price between plan 59 and plan 59 plus"},
		{"Do I get a router with Fiber 500?", "Yes. An R3 router comes with Fiber 500. You return it when you cancel the plan.", "is the router free with Fiber 500\ndo I have to buy my own router"},
		{"What upload speed does Fiber 1000 have?", "Fiber 1000 has an upload speed of up to 500 Mbps and a download speed of up to 1000 Mbps.", "how fast is the upload on fiber one thousand"},
		{"Does Fiber 500 include a TV box?", "No. A T2 TV box can be added to Fiber 500 for 10 yuan a month. Fiber 1000 includes one T2.", "can I get a TV box with my broadband"},
		{"Can I pay my bill at a service hall?", "Yes. You can pay in cash or by card at any service hall. Technicians do not take cash at your home.", "where can I pay in cash\ncan I pay at a shop"},
		{"How do I get an invoice?", "Request an electronic invoice in the NovaNet app under Billing. It arrives by email within 3 business days.", "I need a receipt for my company\nhow do I get a tax invoice"},
		{"Can I keep my number if I switch to NovaNet?", "Yes. Porting your number in is free and takes 1 to 3 business days.", "bring my own number\ntransfer my number from another carrier"},
		{"What is the late fee on an overdue bill?", "From 1 September 2026 there is a 10-day grace period. After that the late fee is 1 percent of the overdue amount.", "how much do you charge if I pay late\nis there a penalty for late payment"},
		{"How many days do I have to cancel after signing up?", "You have 7 days from activation to cancel without an early termination fee.", "cooling off period\ncan I cancel right after I sign up"},
		{"What is an eSIM?", "An eSIM is a SIM built into your phone. You switch it on by scanning a QR code in the NovaNet app instead of inserting a card.", "what is a digital SIM\nhow does eSIM work"},
		{"Can I use an eSIM on Senior Care?", "No. Senior Care is only issued on a physical SIM card. Basic is also physical SIM only.", "does senior care support esim"},
		{"How long is an eSIM QR code valid?", "An eSIM QR code is valid for 24 hours and works on one phone only.", "my eSIM QR code stopped working\nQR code expired"},
		{"Can my family share my data?", "Plan 99 includes two extra SIMs that share the data of the main SIM. On Plan 59 Plus you can add one extra SIM for 10 yuan a month.", "can I add my wife to my plan\nfamily sharing"},
		{"Who can get Senior Care?", "Senior Care costs 19 yuan a month and is for customers aged 65 or older.", "plan for old people\nis there a discount for my parents"},
		{"How do I turn on data roaming?", "Buy a roaming pack in the NovaNet app and switch on data roaming on your phone. Roaming is off by default.", "using my phone abroad\ntravelling next week what do I do"},
		{"What is a Booster pack?", "A Booster pack adds data to your plan. Booster 10 gives 10 GB for 15 yuan and Booster 30 gives 30 GB for 35 yuan.", "I ran out of data\nbuy more data"},
		{"How long is the warranty on my router?", "Routers supplied by NovaNet have a 24-month warranty from the installation date.", "my router broke is it covered"},
		{"How do I reset the R3?", "Hold the Reset pinhole on the back of the R3 for 5 seconds until the lights blink.", "factory reset R3\nreset the entry router"},
		{"How do I reset the R5?", "Hold the Reset button under the label of the R5 for 8 seconds until the lights blink.", "factory reset R5\nR5 reset button"},
		{"How do I reset the R5 Pro?", "Hold the Reset button under the label of the R5 Pro for 10 seconds until the lights blink.", "factory reset R5 Pro\nR5 Pro reset button"},
		{"Can I pause my line while I travel?", "Yes. A line can be paused for up to 90 days a year for 5 yuan a month. Calls and data do not work while it is paused.", "hold my number for a few months\nvacation hold"},
		{"How do I cancel my plan?", "Tell us in the NovaNet app or at a service hall. The cancellation takes effect 30 days after your request.", "close my account\nstop my service"},
		{"Do you have a student plan?", "No. There is no student plan. Students can join the student data bonus while it runs.", "student discount"},
		{"What does a red Internet light mean?", "A red Internet light means the router cannot reach the internet. Check the cable to the optical modem and restart the router.", "router red light\nno internet red light on the router"},
	}
	enQA := nvBook{key: "en/faq/customer-care-quick-answers.xlsx", sheets: []nvSheet{
		{"Quick answers", en},
		{"Change log", [][]string{{"Date", "Change"}, {"2026-09-01", "Late fee answer updated to the new policy"}, {"2026-07-01", "Plan answers checked against the 2026 plan sheets"}}},
	}, qa: qaMapping("Quick answers", "Question", "Answer", "Other ways to ask", "EN")}
	nvAddBook(enQA)

	zh := [][]string{
		{"星联宽带客服快速问答"},
		{"问题", "答复", "其他问法"},
		{"怎么联系客服？", "可以随时在星联宽带应用里在线咨询，也可以每天8:00至22:00拨打客服电话。", "我想找人工\n客服几点上班\n有没有在线客服"},
		{"可以在线换套餐吗？", "可以。在应用的“我的套餐”里选择新套餐。升档立即生效，降档从下个账单日起生效。", "怎么换套餐\n我想升级套餐\n能换成便宜一点的吗"},
		{"怎么查流量用了多少？", "打开星联宽带应用的“用量”，可以看到账单日以来用了多少流量、分钟和漫游。", "我还剩多少流量\n在哪儿看剩余流量"},
		{"59元Plus套餐比59元套餐便宜吗？", "不便宜，两个套餐月费都是59元。Plus套餐流量和分钟更多，但合约期更长。", "五十九元plus套餐多少钱\n59和59加有什么价格区别"},
		{"光纤500送路由器吗？", "送，光纤500赠送R3路由器，取消套餐时归还。", "光纤500路由器要钱吗\n要不要自己买路由器"},
		{"光纤1000的上行速率是多少？", "光纤1000上行速率最高500Mbps，下行最高1000Mbps。", "光纤一千上传多快"},
		{"光纤500含电视盒吗？", "不含。光纤500可以另加T2电视盒，每月10元；光纤1000含一台T2。", "宽带能送电视盒吗"},
		{"可以到营业厅缴费吗？", "可以。任意营业厅都可以用现金或银行卡缴费，上门师傅不收现金。", "哪里可以交现金\n能去店里交话费吗"},
		{"怎么开发票？", "在星联宽带应用的“账单”里申请电子发票，3个工作日内发到邮箱。", "我要报销怎么办\n公司抬头发票怎么开"},
		{"换到星联宽带可以保留号码吗？", "可以。携号转入免费，需要1到3个工作日。", "能带着号码过来吗\n别家的号码转过来"},
		{"逾期缴费滞纳金是多少？", "2026年9月1日起，到期后有10天宽限期，之后滞纳金为逾期金额的百分之一。", "晚交话费罚多少\n晚交有没有违约金"},
		{"办理后几天内可以无理由取消？", "开通后7天内取消，不收提前解约费。", "冷静期多久\n刚办完能退吗"},
		{"eSIM是什么？", "eSIM是内置在手机里的SIM卡，在应用里扫二维码就能开通，不用插卡。", "什么是电子SIM卡\neSIM怎么用"},
		{"敬老套餐可以用eSIM吗？", "不可以，敬老套餐只提供实体SIM卡，基础套餐也一样。", "敬老套餐支持eSIM吗"},
		{"eSIM二维码有效期多久？", "eSIM二维码24小时有效，只能在一台手机上使用。", "eSIM二维码失效了\n二维码过期了"},
		{"家人可以共享我的流量吗？", "99元套餐赠送两张副卡，与主卡共享流量；59元Plus套餐可加办一张副卡，每月10元。", "能给老婆办副卡吗\n家庭共享"},
		{"谁可以办敬老套餐？", "敬老套餐每月19元，面向年满65周岁的客户。", "有没有老人套餐\n给爸妈办有优惠吗"},
		{"怎么开启数据漫游？", "在应用里购买漫游包，并在手机上打开数据漫游，漫游默认是关闭的。", "出国怎么上网\n下周出差要做什么准备"},
		{"加油包是什么？", "加油包用来补充流量。加油包10是10GB，15元；加油包30是30GB，35元。", "流量不够用了\n买点流量"},
		{"路由器保修多久？", "星联宽带提供的路由器自安装之日起保修24个月。", "路由器坏了保修吗"},
		{"R3路由器怎么复位？", "按住R3背面的Reset小孔5秒，直到指示灯闪烁。", "R3恢复出厂设置\n入门款路由器复位"},
		{"R5路由器怎么复位？", "按住R5标签下方的Reset按钮8秒，直到指示灯闪烁。", "R5恢复出厂设置\nR5复位键"},
		{"R5 Pro路由器怎么复位？", "按住R5 Pro标签下方的Reset按钮10秒，直到指示灯闪烁。", "R5 Pro恢复出厂设置\nR5 Pro复位键"},
		{"出门旅行可以暂停号码吗？", "可以。号码每年最多暂停90天，每月5元，暂停期间不能通话和上网。", "号码保留几个月\n出国长住保号"},
		{"怎么取消套餐？", "在星联宽带应用或营业厅提出，申请后30天生效。", "我要销户\n不想用了怎么停"},
		{"有学生套餐吗？", "没有学生套餐。学生可以在活动期间参加学生流量福利。", "学生优惠"},
		{"Internet灯是红的是什么意思？", "红灯表示路由器连不上互联网，请检查连光猫的网线并重启路由器。", "路由器红灯\n路由器上红灯没有网"},
	}
	nvAddBook(nvBook{key: "zh/faq/客服快速问答.xlsx", sheets: []nvSheet{
		{"快速问答", zh},
		{"更新记录", [][]string{{"日期", "更新内容"}, {"2026-09-01", "滞纳金答复已按新政策更新"}, {"2026-07-01", "套餐答复已对照2026年资费说明核对"}}},
	}, qa: qaMapping("快速问答", "问题", "答复", "其他问法", "ZH")})
}
