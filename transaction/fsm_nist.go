package transaction

import (
	"fmt"

	"github.com/qnsoft/live_gb28181/sip"
)

/*
                         |Request received
                         |pass to TU
                         V
                   +-----------+
                   |           |
                   | Trying    |-------------+
                   |           |             |
                   +-----------+             |200-699 from TU
                         |                   |send response
                         |1xx from TU        |
                         |send response      |
                         |                   |
      Request            V      1xx from TU  |
      send response+-----------+send response|
          +--------|           |--------+    |
          |        | Proceeding|        |    |
          +------->|           |<-------+    |
   +<--------------|           |             |
   |Trnsprt Err    +-----------+             |
   |Inform TU            |                   |
   |                     |                   |
   |                     |200-699 from TU    |
   |                     |send response      |
   |  Request            V                   |
   |  send response+-----------+             |
   |      +--------|           |             |
   |      |        | Completed |<------------+
   |      +------->|           |
   +<--------------|           |
   |Trnsprt Err    +-----------+
   |Inform TU            |
   |                     |Timer J fires
   |                     |-
   |                     |
   |                     V
   |               +-----------+
   |               |           |
   +-------------->| Terminated|
                   |           |
                   +-----------+

       Figure 8: non-INVITE server transaction

*/

func nist_rcv_request(t *Transaction, evt Event, m *sip.Message) error {
	fmt.Println("rcv request: ", m.GetMethod())
	fmt.Println("transaction state: ", t.state.String())
	if t.state != NIST_PRE_TRYING {
		fmt.Println("rcv request retransmission,do response")
		if t.lastResponse != nil {
			err := t.SipSend(t.lastResponse)
			if err != nil {
				//transport error
				return err
			}
		}
		return nil
	} else {
		t.origRequest = m
		t.state = NIST_TRYING
		t.isReliable = m.IsReliable()
	}

	return nil
}

func nist_snd_1xx(t *Transaction, evt Event, m *sip.Message) error {
	t.lastResponse = m
	err := t.SipSend(t.lastResponse)
	if err != nil {
		return err
	}

	t.state = NIST_PROCEEDING
	return nil
}

func nist_snd_23456xx(t *Transaction, evt Event, m *sip.Message) error {
	t.lastResponse = m
	if err := t.SipSend(t.lastResponse); err != nil {
		return err
	}
	if t.state != NIST_COMPLETED {
		if !t.isReliable {
			t.RunAfter(T1*64, TIMEOUT_J)
		}
	}

	t.state = NIST_COMPLETED
	return nil
}
func osip_nist_timeout_j_event(t *Transaction, evt Event, m *sip.Message) error {
	t.Terminate()
	return nil
}
