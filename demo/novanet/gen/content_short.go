// SPDX-License-Identifier: Apache-2.0

package main

// Short pages: one heading, one answer. The whole document is the answer.

type shortDoc struct {
	slug, zhSlug string
	qEN, aEN     string
	qZH, aZH     string
}

var shortDocs = []shortDoc{
	{"customer-care-hours", "客服服务时间", "What are the customer care hours?", "Customer care by phone is open every day from 8:00 to 22:00. Chat in the NovaNet app is open 24 hours a day.", "客服的服务时间是什么？", "客服电话每天8:00至22:00服务，应用内在线客服全天24小时服务。"},
	{"does-novanet-sell-phones", "是否销售手机", "Does NovaNet sell phones?", "No. NovaNet sells routers, TV boxes and accessories, but not mobile phones.", "星联宽带卖手机吗？", "不卖。星联宽带销售路由器、电视盒和配件，不销售手机。"},
	{"app-cost", "应用是否收费", "Does the NovaNet app cost anything?", "No. The NovaNet app is free to download and to use.", "星联宽带应用收费吗？", "不收费。星联宽带应用免费下载、免费使用。"},
	{"student-discount", "学生优惠", "Is there a permanent student discount?", "No. There is no permanent student discount. Students can join the student data bonus while it runs.", "有长期的学生优惠吗？", "没有长期的学生优惠。学生可以在活动期间参加学生流量福利。"},
	{"sim-size", "SIM卡尺寸", "What size is the NovaNet SIM card?", "The NovaNet SIM is a 3-in-1 card that fits standard, micro and nano slots.", "星联宽带的SIM卡是什么尺寸？", "星联宽带的SIM卡是三合一卡，适用于标准、Micro和Nano卡槽。"},
	{"use-own-router", "自带路由器", "Can I use my own router?", "Yes, behind the NovaNet modem. We can only give support for NovaNet routers.", "可以用自己的路由器吗？", "可以，接在星联宽带光猫后面使用，但我们只能为星联宽带路由器提供技术支持。"},
	{"fixed-ip", "固定IP", "Do home plans have a fixed IP address?", "No. A fixed IP address is only part of business broadband.", "家庭套餐含固定IP地址吗？", "不含。固定IP地址只包含在企业宽带里。"},
	{"family-plan", "家庭套餐", "Is there a family plan?", "NovaNet has no separate family plan. Plan 99 includes two extra SIMs, and extra SIMs can be added to Plan 59 Plus.", "有家庭套餐吗？", "星联宽带没有单独的家庭套餐。99元套餐含两张副卡，59元Plus套餐可以加办副卡。"},
	{"payment-on-due-date", "到期日缴费", "Is paying on the due date still on time?", "Yes. A payment received by the end of the due date is on time and has no late fee.", "到期日当天缴费还算按时吗？", "算。到期日当天结束前收到的款项都算按时，不收滞纳金。"},
	{"language-support", "服务语言", "What languages do you support?", "Customer care speaks Chinese and English.", "客服说哪些语言？", "客服提供中文和英文服务。"},
	{"hotspot-device-limit", "热点连接数", "How many devices can use my hotspot?", "Up to 8 devices can connect to a phone hotspot at the same time.", "手机热点最多能连几台设备？", "手机热点最多同时连接8台设备。"},
	{"lost-router", "路由器丢失", "What if I lose my router?", "A lost router is charged at its replacement price: 300 yuan for the R3 and 600 yuan for the R5 Pro.", "路由器丢了怎么办？", "路由器丢失按设备更换价收费：R3为300元，R5 Pro为600元。"},
	{"bill-in-english", "英文账单", "Can I get my bill in English?", "Yes. Choose English as the bill language in the NovaNet app under Settings.", "可以收到英文账单吗？", "可以。在星联宽带应用的“设置”里把账单语言选为英文即可。"},
	{"number-porting-fee", "携号转入费用", "Is porting my number to NovaNet free?", "Yes. Porting a number in to NovaNet is free of charge.", "携号转入星联宽带收费吗？", "不收费。携号转入星联宽带免费。"},
	{"voicemail-length", "语音信箱时长", "How long can a voicemail message be?", "A voicemail message can be up to 3 minutes long.", "语音信箱每条留言最长多久？", "每条语音留言最长3分钟。"},
	{"wifi-name-change", "修改Wi-Fi名称", "Can I change the Wi-Fi name?", "Yes. Change the Wi-Fi name in the NovaNet app under your router settings.", "可以修改Wi-Fi名称吗？", "可以。在星联宽带应用的路由器设置里修改Wi-Fi名称。"},
}

func init() {
	for _, s := range shortDocs {
		nvPairNamed("short/"+s.slug, "short/"+s.zhSlug, "# "+s.qEN+"\n"+s.aEN+"\n", "# "+s.qZH+"\n"+s.aZH+"\n")
	}
}
