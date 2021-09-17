package transaction

import (
	"fmt"
	"net"
	"strings"

	"github.com/qnsoft/live_gb28181/sip"
)

//=====================================================sip message utils
//The branch ID parameter in the Via header field values serves as a transaction identifier,
//and is used by proxies to detect loops.
//The branch parameter in the topmost Via header field of the request
//     is examined. If it is present and begins with the magic cookie
//     "z9hG4bK", the request was generated by a client transaction
//     compliant to this specification.
//参考RFC3261
func getMessageTransactionID(m *sip.Message) string {
	if m.GetMethod() == sip.ACK {
		//TODO：在匹配服务端事物的ACK中，创建事务的请求的方法为INVITE。所以ACK消息匹配事物的时候需要注意？？？？
	}
	return string(m.GetMethod()) + "_" + m.GetBranch()
}

//根据收到的响应的消息的状态码，获取事件
func getInComingMessageEvent(m *sip.Message) Event {
	//request：根据请求方法来确认事件
	if m.IsRequest() {
		method := m.GetMethod()
		if method == sip.INVITE {
			return RCV_REQINVITE
		} else if method == sip.ACK {
			return RCV_REQACK
		} else {
			return RCV_REQUEST
		}
	}

	//response：根据状态码来确认事件
	status := m.StartLine.Code
	if status >= 100 && status < 200 {
		return RCV_STATUS_1XX
	}

	if status >= 200 && status < 300 {
		return RCV_STATUS_2XX
	}
	if status >= 300 {
		return RCV_STATUS_3456XX
	}

	return UNKNOWN_EVT
}

//根据发出的响应的消息的状态码，获取事件
func getOutGoingMessageEvent(m *sip.Message) Event {
	//request:get event by method
	if m.IsRequest() {
		method := m.GetMethod()
		if method == sip.INVITE {
			return SND_REQINVITE
		} else if method == sip.ACK {
			return SND_REQACK
		} else {
			return SND_REQUEST
		}
	}

	//response:get event by status
	status := m.StartLine.Code
	if status >= 100 && status < 200 {
		return SND_STATUS_1XX
	}

	if status >= 200 && status < 300 {
		return SND_STATUS_2XX
	}
	if status >= 300 {
		return SND_STATUS_3456XX
	}

	return UNKNOWN_EVT
}

func checkMessage(msg *sip.Message) error {
	//TODO:sip消息解析成功之后，检查必要元素，如果失败，则返回 ErrorCheckMessage

	//检查头域字段：callID  via  startline 等
	//检查seq、method等
	//不可以有router？
	//是否根据消息是接收还是发送检查？
	if msg == nil {
		return ErrorCheck
	}
	return nil
}

//fix via header,add send-by info,
func fixReceiveMessageViaParams(msg *sip.Message, addr net.Addr) {
	rport := msg.Via.Params["rport"]
	if rport == "" || rport == "0" || rport == "-1" {
		arr := strings.Split(addr.String(), ":")
		if len(arr) == 2 {
			msg.Via.Params["rport"] = arr[1]
			if msg.Via.Host != arr[0] {
				msg.Via.Params["received"] = arr[0]
			}
		} else {
			//TODO：数据报的地址错误？？
			fmt.Println("packet handle > invalid addr :", addr.String())
		}
	} else {
		fmt.Println("sip message has have send-by info:", msg.Via.GetSendBy())
	}
}
