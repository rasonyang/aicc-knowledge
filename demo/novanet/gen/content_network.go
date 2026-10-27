// SPDX-License-Identifier: Apache-2.0

package main

import (
	"fmt"
)

// Service-area records, service hall pages and maintenance notices for 30
// invented districts. They are the many similar documents of a real knowledge
// base: they read alike, differ in one fact each, and make a district question
// land on the right one of thirty near-identical pages.

type district struct {
	zh, en string
	lead   int    // installation lead time in business days
	g10    bool   // Fiber 1000 available
	status string // OPEN, SOON, NO
	soonEN string
	soonZH string
	hall   bool
	plaza  string
	plazaZ string
}

var districts = func() []district {
	names := [][2]string{
		{"星澜湾", "Starwave Bay"}, {"云栖里", "Cloudperch Quarter"}, {"碧荷坊", "Greenlotus Ward"}, {"松涛岭", "Pinebreeze Heights"},
		{"银杏里", "Ginkgo Row"}, {"望月台", "Moongaze Terrace"}, {"听雨轩", "Rainsong Court"}, {"锦帆港", "Brocadesail Harbor"},
		{"白鹭洲", "Egret Isle"}, {"紫藤坞", "Wisteria Dock"}, {"琥珀街", "Amber Mile"}, {"青岚谷", "Mistgreen Valley"},
		{"栖霞园", "Duskglow Gardens"}, {"莲心湖", "Lotuscore Lake"}, {"枫桥里", "Mapleford Quarter"}, {"墨香堡", "Inkscent Hill"},
		{"玉兰苑", "Magnolia Court"}, {"流芳渡", "Driftbloom Crossing"}, {"海棠湾", "Crabapple Bay"}, {"石榴巷", "Pomegranate Lane"},
		{"烟波渚", "Hazewater Shoal"}, {"竹影坪", "Bambooshade Flats"}, {"梧桐里", "Parasol Quarter"}, {"丹桂坊", "Osmanthus Ward"},
		{"凌云坡", "Skyreach Slope"}, {"澄心岛", "Clearheart Island"}, {"鹤鸣山", "Cranecall Hill"}, {"翠微镇", "Verdantpeak Town"},
		{"浮光渡", "Glimmerferry"}, {"晴川里", "Sunbrook Quarter"},
	}
	plazas := [][2]string{{"Skyline Plaza", "天际广场"}, {"Harbor Mall", "港湾商城"}, {"Lantern Center", "灯塔中心"}, {"Orchard Court", "果园汇"}, {"Bluebird Plaza", "青鸟广场"}}
	var out []district
	for i, n := range names {
		d := district{zh: n[0], en: n[1], lead: 2 + (i*5)%6, g10: i%3 != 2, status: "OPEN", hall: i%2 == 0}
		switch i {
		case 7:
			d.status, d.soonEN, d.soonZH = "SOON", "March 2027", "2027年3月"
		case 19:
			d.status, d.soonEN, d.soonZH = "SOON", "January 2027", "2027年1月"
		case 26:
			d.status, d.soonEN, d.soonZH = "SOON", "June 2027", "2027年6月"
		case 13, 28:
			d.status = "NO"
		}
		if d.status != "OPEN" {
			d.g10 = false
		}
		p := plazas[i%len(plazas)]
		d.plaza, d.plazaZ = p[0], p[1]
		out = append(out, d)
	}
	return out
}()

var monthsEN = []string{"", "January", "February", "March", "April", "May", "June", "July", "August", "September", "October", "November", "December"}

func dateEN(y, m, d int) string { return fmt.Sprintf("%d %s %d", d, monthsEN[m], y) }
func dateZH(y, m, d int) string { return fmt.Sprintf("%d年%d月%d日", y, m, d) }

func (d district) coverageEN() (f500, f1000 string) {
	switch d.status {
	case "SOON":
		return "Fiber is not available in " + d.en + " yet. We expect to open the area in " + d.soonEN + ".",
			"Fiber 1000 will follow after the area opens."
	case "NO":
		return "NovaNet home broadband is not available in " + d.en + " and there is no opening date yet.",
			"Fiber 1000 is not available."
	}
	f500 = "Fiber 500 is available throughout " + d.en + "."
	if d.g10 {
		return f500, "Fiber 1000 is available in " + d.en + " too, because the area has been upgraded to 10G fiber."
	}
	return f500, "Fiber 1000 is not available in " + d.en + " yet, because the area has not been upgraded to 10G fiber."
}

func (d district) coverageZH() (f500, f1000 string) {
	switch d.status {
	case "SOON":
		return d.zh + "暂时还不能办理光纤宽带，预计" + d.soonZH + "开通。", "开通后光纤1000会随后推出。"
	case "NO":
		return d.zh + "目前无法办理星联宽带家庭宽带，暂无开通时间。", "光纤1000暂不可办。"
	}
	f500 = d.zh + "全区可以办理光纤500。"
	if d.g10 {
		return f500, d.zh + "已升级万兆光网，也可以办理光纤1000。"
	}
	return f500, d.zh + "还没有升级万兆光网，暂时不能办理光纤1000。"
}

// hallHours returns the opening hours of the hi-th service hall.
func hallHours(hi int) (en, zh string) {
	switch hi % 3 {
	case 0:
		return "The hall is open every day from 9:30 to 18:30.", "营业厅每天9:30至18:30营业。"
	case 1:
		return "The hall is open Monday to Friday from 9:00 to 18:00 and on Saturday and Sunday from 10:00 to 17:00.", "营业厅周一至周五9:00至18:00营业，周六和周日10:00至17:00营业。"
	}
	return "The hall is open Tuesday to Sunday from 10:00 to 19:00 and is closed on Monday.", "营业厅周二至周日10:00至19:00营业，周一休息。"
}

func init() {
	for i, d := range districts {
		slug := fmt.Sprintf("area-%02d", i+1)
		f5en, f10en := d.coverageEN()
		f5zh, f10zh := d.coverageZH()
		leadEN := fmt.Sprintf("Installation in %s usually takes %d business days after the order is confirmed.", d.en, d.lead)
		leadZH := fmt.Sprintf("%s的宽带订单确认后，一般%d个工作日内安装完成。", d.zh, d.lead)
		if d.status != "OPEN" {
			leadEN = "There is no installation lead time yet because the area is not open."
			leadZH = "该区域尚未开通，暂时没有安装时效。"
		}
		hallEN, hallZH := "There is no service hall in "+d.en+". The nearest one is listed in the NovaNet app.", d.zh+"没有营业厅，最近的营业厅请在星联宽带应用里查看。"
		if d.hall {
			hallEN = d.en + " has a service hall on the ground floor of " + d.plaza + "."
			hallZH = d.zh + "的营业厅在" + d.plazaZ + "一楼。"
		}
		var en, zh string
		switch i % 3 {
		case 0:
			en = fmt.Sprintf(`# %[1]s service area
Region: %[1]s
## Broadband in %[1]s?
%[2]s
## Fiber 1000 in %[1]s?
%[3]s
## Install time in %[1]s?
%[4]s
## Service hall in %[1]s?
%[5]s
`, d.en, f5en, f10en, leadEN, hallEN)
			zh = fmt.Sprintf(`# %[1]s服务区域
所属区域：%[1]s
## %[1]s可以办理星联宽带吗？
%[2]s
## %[1]s可以办理光纤1000吗？
%[3]s
## %[1]s宽带安装要多久？
%[4]s
## %[1]s有营业厅吗？
%[5]s
`, d.zh, f5zh, f10zh, leadZH, hallZH)
		case 1:
			tF5, tF10, tLead := "Available", "Available", fmt.Sprintf("%d business days", d.lead)
			zF5, zF10, zLead := "可办理", "可办理", fmt.Sprintf("%d个工作日", d.lead)
			if d.status != "OPEN" {
				tF5, tF10, tLead = "Not yet", "Not yet", "none"
				zF5, zF10, zLead = "暂不可办", "暂不可办", "无"
			} else if !d.g10 {
				tF10, zF10 = "Not yet", "暂不可办"
			}
			en = fmt.Sprintf(`# Service area record %[1]s
| Item | Detail |
| District | %[1]s |
| Fiber 500 | %[2]s |
| Fiber 1000 | %[3]s |
| Installation lead time | %[4]s |
## Notes for %[1]s
%[5]s %[6]s
`, d.en, tF5, tF10, tLead, f5en, hallEN)
			zh = fmt.Sprintf(`# 服务区域记录 %[1]s
| 项目 | 内容 |
| 区域 | %[1]s |
| 光纤500 | %[2]s |
| 光纤1000 | %[3]s |
| 安装时效 | %[4]s |
## %[1]s说明
%[5]s%[6]s
`, d.zh, zF5, zF10, zLead, f5zh, hallZH)
		default:
			en = fmt.Sprintf(`# %[1]s coverage
## Coverage in %[1]s
%[2]s %[3]s
## Installation lead time
%[4]s
## Service hall
%[5]s
`, d.en, f5en, f10en, leadEN, hallEN)
			zh = fmt.Sprintf(`# %[1]s覆盖与安装
## %[1]s的覆盖情况
%[2]s%[3]s
## 安装时效
%[4]s
## 营业厅
%[5]s
`, d.zh, f5zh, f10zh, leadZH, hallZH)
		}
		nvPairNamed("network/service-areas/"+slug, "network/服务区域/"+d.zh+"服务区域", en, zh)
	}

	// Service halls: opening hours and services.
	hi := 0
	for i, d := range districts {
		if !d.hall {
			continue
		}
		hi++
		hoursEN, hoursZH := hallHours(hi)
		devEN, devZH := "Device pickup, SIM replacement, bill payment and number porting are all handled here.", "这里可以办理设备领取、补卡、缴费和携号转网。"
		if hi%4 == 0 {
			devEN, devZH = "SIM replacement, bill payment and number porting are handled here. Devices are not stocked, so please order devices in the app.", "这里可以办理补卡、缴费和携号转网。本厅不备货设备，请在应用里订购设备。"
		}
		en := fmt.Sprintf(`# %[1]s Service Hall
Location: %[2]s
## %[1]s hall opening hours
%[3]s
## Services at the %[1]s hall
%[4]s
## %[1]s hall on holidays
On the day before a public holiday the hall closes at 15:00. It is closed on the holiday itself.
`, d.en, d.plaza, hoursEN, devEN)
		zh := fmt.Sprintf(`# %[1]s营业厅
位置：%[2]s
## %[1]s营业厅的营业时间是什么？
%[3]s
## 在%[1]s营业厅可以办什么？
%[4]s
## %[1]s营业厅法定节假日营业吗？
法定节假日前一天营业到15:00，节假日当天休息。
`, d.zh, d.plazaZ, hoursZH, devZH)
		nvPairNamed(fmt.Sprintf("stores/service-hall-%02d", i+1), "stores/营业厅/"+d.zh+"营业厅", en, zh)
	}

	// Maintenance notices.
	type notice struct {
		di      int
		y, m, d int
		kind    string // FIBER, MOBILE, UPGRADE
		hours   int
	}
	notices := []notice{
		{0, 2026, 11, 8, "FIBER", 4}, {1, 2026, 3, 14, "MOBILE", 3}, {2, 2026, 12, 2, "UPGRADE", 6}, {3, 2026, 2, 20, "FIBER", 2},
		{4, 2026, 11, 15, "MOBILE", 5}, {5, 2026, 4, 9, "FIBER", 3}, {6, 2026, 12, 12, "UPGRADE", 8}, {8, 2026, 5, 22, "MOBILE", 2},
		{9, 2026, 11, 22, "FIBER", 4}, {10, 2026, 6, 6, "UPGRADE", 6}, {11, 2026, 12, 20, "MOBILE", 3}, {12, 2026, 3, 28, "FIBER", 5},
	}
	for n, nt := range notices {
		d := districts[nt.di]
		var whatEN, whatZH, whoEN, whoZH, doEN, doZH string
		switch nt.kind {
		case "FIBER":
			whatEN = fmt.Sprintf("We will service the fiber cabinets in %s.", d.en)
			whatZH = fmt.Sprintf("我们将对%s的光缆交接箱进行检修。", d.zh)
			whoEN, whoZH = "Home broadband customers in the district may lose their connection.", "该区域的家庭宽带客户可能会断网。"
			doEN, doZH = "Nothing. The connection returns by itself. Please restart the router if the Internet light stays red afterwards.", "无需操作，网络会自动恢复。如果之后Internet灯仍是红色，请重启路由器。"
		case "MOBILE":
			whatEN = fmt.Sprintf("We will maintain a mobile tower in %s.", d.en)
			whatZH = fmt.Sprintf("我们将对%s的一座基站进行维护。", d.zh)
			whoEN, whoZH = "Mobile customers near the tower may have weaker signal and slower data. Calls fall back to a neighboring tower.", "基站附近的手机客户信号可能变弱、网速变慢，通话会切换到邻近基站。"
			doEN, doZH = "Nothing. If you need a stable connection, use Wi-Fi during the work.", "无需操作。如需稳定的网络，施工期间请使用Wi-Fi。"
		default:
			whatEN = fmt.Sprintf("We are upgrading the fiber network in %s to 10G, so Fiber 1000 can be offered.", d.en)
			whatZH = fmt.Sprintf("我们正在把%s的光纤网络升级到万兆，以便提供光纤1000。", d.zh)
			whoEN, whoZH = "Home broadband customers in the district have a short break while the line is switched.", "该区域的家庭宽带客户在线路切换时会短暂断网。"
			doEN, doZH = "Nothing. After the upgrade, customers of Fiber 500 can order Fiber 1000 in the NovaNet app.", "无需操作。升级完成后，光纤500客户可以在星联宽带应用里申请光纤1000。"
		}
		en := fmt.Sprintf(`# Notice N-%03[3]d, %[1]s work
Issued by: Network Operations
## What is the planned work?
%[4]s The work is planned for %[2]s.
## How long will the work take?
The work takes up to %[5]d hours. It starts at 1:00 in the morning.
## Who is affected?
%[6]s
## What do I need to do?
%[7]s
`, d.en, dateEN(nt.y, nt.m, nt.d), n+1, whatEN, nt.hours, whoEN, doEN)
		zh := fmt.Sprintf(`# 公告：%[1]s将于%[2]s施工
公告编号：N-%03[3]d
## %[1]s将进行什么施工？
%[4]s施工时间定于%[2]s。
## 施工要多久？
施工最长%[5]d小时，凌晨1:00开始。
## 哪些客户受影响？
%[6]s
## 我需要做什么？
%[7]s
`, d.zh, dateZH(nt.y, nt.m, nt.d), n+1, whatZH, nt.hours, whoZH, doZH)
		nvPairNamed(fmt.Sprintf("network/notices/notice-%03d", n+1), fmt.Sprintf("network/公告/公告%03d_%s施工", n+1, d.zh), en, zh)
	}
}
