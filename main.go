package gb28181

import (
	"bytes"
	"encoding/xml"
	"log"
	"net"
	"net/http"
	"strconv"
	"sync"
	"time"

	. "github.com/logrusorgru/aurora"
	"github.com/pion/rtp"
	"github.com/qnsoft/live_gb28181/sip"
	"github.com/qnsoft/live_gb28181/transaction"
	"github.com/qnsoft/live_gb28181/utils"
	"github.com/qnsoft/live_sdk"
	. "github.com/qnsoft/live_utils"
	"golang.org/x/net/html/charset"
)

var (
	Devices    sync.Map
	Ignores    = make(map[string]struct{})
	publishers Publishers
)

func FindChannel(deviceId string, channelId string) (c *Channel) {
	if v, ok := Devices.Load(deviceId); ok {
		d := v.(*Device)
		d.channelMutex.RLock()
		c = d.channelMap[channelId]
		d.channelMutex.RUnlock()
	}
	return
}

type Publishers struct {
	data map[uint32]*Publisher
	sync.RWMutex
}

func (p *Publishers) Add(key uint32, pp *Publisher) {
	p.Lock()
	p.data[key] = pp
	p.Unlock()
}
func (p *Publishers) Remove(key uint32) {
	p.Lock()
	delete(p.data, key)
	p.Unlock()
}
func (p *Publishers) Get(key uint32) *Publisher {
	p.RLock()
	defer p.RUnlock()
	return p.data[key]
}

var config = struct {
	Serial          string
	Realm           string
	ListenAddr      string
	Expires         int
	MediaPort       uint16
	AutoInvite      bool
	AutoUnPublish   bool
	Ignore          []string
	CatalogInterval int
	PreFetchRecord  bool
}{"34020000002000000001", "3402000000", "127.0.0.1:5060", 3600, 58200, false, true, nil, 30, false}

func init() {
	engine.InstallPlugin(&engine.PluginConfig{
		Name:   "LiveGB28181",
		Config: &config,
		Run:    run,
	})
	publishers.data = make(map[uint32]*Publisher)
}

func run() {
	ipAddr, err := net.ResolveUDPAddr("", config.ListenAddr)
	if err != nil {
		log.Fatal(err)
	}
	Print(Green("server gb28181 start at"), BrightBlue(config.ListenAddr))
	for _, id := range config.Ignore {
		Ignores[id] = struct{}{}
	}
	config := &transaction.Config{
		SipIP:             ipAddr.IP.String(),
		SipPort:           uint16(ipAddr.Port),
		SipNetwork:        "UDP",
		Serial:            config.Serial,
		Realm:             config.Realm,
		AckTimeout:        10,
		MediaIP:           ipAddr.IP.String(),
		RegisterValidity:  config.Expires,
		RegisterInterval:  60,
		HeartbeatInterval: 60,
		HeartbeatRetry:    3,
		AudioEnable:       true,
		WaitKeyFrame:      true,
		MediaIdleTimeout:  30,
		CatalogInterval:   config.CatalogInterval,
	}
	http.HandleFunc("/api/gb28181/query/records", func(w http.ResponseWriter, r *http.Request) {
		CORS(w, r)
		id := r.URL.Query().Get("id")
		channel := r.URL.Query().Get("channel")
		startTime := r.URL.Query().Get("startTime")
		endTime := r.URL.Query().Get("endTime")
		if c := FindChannel(id, channel); c != nil {
			w.WriteHeader(c.QueryRecord(startTime, endTime))
		} else {
			w.WriteHeader(404)
		}
	})
	http.HandleFunc("/api/gb28181/list", func(w http.ResponseWriter, r *http.Request) {
		CORS(w, r)
		sse := NewSSE(w, r.Context())
		for {
			var list []*Device
			Devices.Range(func(key, value interface{}) bool {
				device := value.(*Device)
				if time.Since(device.UpdateTime) > time.Duration(config.RegisterValidity)*time.Second {
					Devices.Delete(key)
				} else {
					list = append(list, device)
				}
				return true
			})
			sse.WriteJSON(list)
			select {
			case <-time.After(time.Second * 5):
			case <-sse.Done():
				return
			}
		}
	})
	http.HandleFunc("/api/gb28181/control", func(w http.ResponseWriter, r *http.Request) {
		CORS(w, r)
		id := r.URL.Query().Get("id")
		channel := r.URL.Query().Get("channel")
		ptzcmd := r.URL.Query().Get("ptzcmd")
		if c := FindChannel(id, channel); c != nil {
			w.WriteHeader(c.Control(ptzcmd))
		} else {
			w.WriteHeader(404)
		}
	})
	http.HandleFunc("/api/gb28181/invite", func(w http.ResponseWriter, r *http.Request) {
		CORS(w, r)
		query := r.URL.Query()
		id := query.Get("id")
		channel := r.URL.Query().Get("channel")
		startTime := query.Get("startTime")
		endTime := query.Get("endTime")
		if c := FindChannel(id, channel); c != nil {
			if startTime == "" && c.LivePublisher != nil {
				w.WriteHeader(304) //直播流已存在
			} else {
				w.WriteHeader(c.Invite(startTime, endTime))
			}
		} else {
			w.WriteHeader(404)
		}
	})
	http.HandleFunc("/api/gb28181/bye", func(w http.ResponseWriter, r *http.Request) {
		CORS(w, r)
		id := r.URL.Query().Get("id")
		channel := r.URL.Query().Get("channel")
		if c := FindChannel(id, channel); c != nil {
			w.WriteHeader(c.Bye())
		} else {
			w.WriteHeader(404)
		}
	})
	s := transaction.NewCore(config)
	s.OnRegister = func(msg *sip.Message) {
		id := msg.From.Uri.UserInfo()
		d := &Device{
			ID:           id,
			RegisterTime: time.Now(),
			UpdateTime:   time.Now(),
			Status:       string(sip.REGISTER),
			Core:         s,
			from:         &sip.Contact{Uri: msg.StartLine.Uri, Params: make(map[string]string)},
			to:           msg.To,
			Addr:         msg.Via.GetSendBy(),
			SipIP:        config.MediaIP,
			channelMap:   make(map[string]*Channel),
		}
		if old, ok := Devices.Load(id); !ok {
			go d.Query()
		} else {
			oldD := old.(*Device)
			d.RegisterTime = oldD.RegisterTime
			d.channelMap = oldD.channelMap
			d.UpdateChannelsDevice()
			d.Status = oldD.Status
		}
		Devices.Store(id, d)
	}
	s.OnMessage = func(msg *sip.Message) bool {
		if v, ok := Devices.Load(msg.From.Uri.UserInfo()); ok {
			d := v.(*Device)
			if d.Status == string(sip.REGISTER) {
				d.Status = "ONLINE"
			}
			d.UpdateTime = time.Now()
			temp := &struct {
				XMLName    xml.Name
				CmdType    string
				DeviceID   string
				DeviceList []*Channel `xml:"DeviceList>Item"`
				RecordList []*Record  `xml:"RecordList>Item"`
			}{}
			decoder := xml.NewDecoder(bytes.NewReader([]byte(msg.Body)))
			decoder.CharsetReader = charset.NewReaderLabel
			err := decoder.Decode(temp)
			if err != nil {
				err = utils.DecodeGbk(temp, []byte(msg.Body))
				log.Printf("decode catelog err: %s", err)
			}
			switch temp.XMLName.Local {
			case "Notify":
				if d.Channels == nil {
					go d.Query()
				}
			case "Response":
				switch temp.CmdType {
				case "Catalog":
					d.UpdateChannels(temp.DeviceList)
				case "RecordInfo":
					d.UpdateRecord(temp.DeviceID, temp.RecordList)
				}
			}
			return true
		}
		return false
	}
	//OnStreamClosedHooks.AddHook(func(stream *Stream) {
	//	Devices.Range(func(key, value interface{}) bool {
	//		device:=value.(*Device)
	//		for _,channel := range device.Channels {
	//			if stream.StreamPath == channel.RecordSP {
	//
	//			}
	//		}
	//	})
	//})
	go listenMedia()
	go queryCatalog(config)
	s.Start()
}
func listenMedia() {
	networkBuffer := 1048576
	var rtpPacket rtp.Packet
	addr, err := net.ResolveUDPAddr("udp", ":"+strconv.Itoa(int(config.MediaPort)))
	if err != nil {
		log.Fatalf("udp server ResolveUDPAddr MediaPort:%d error, %v", config.MediaPort, err)
	}
	conn, err := net.ListenUDP("udp", addr)
	if err != nil {
		log.Fatalf("udp server ListenUDP MediaPort:%d error, %v", config.MediaPort, err)
	}
	if err = conn.SetReadBuffer(networkBuffer); err != nil {
		Printf("udp server video conn set read buffer error, %v", err)
	}
	if err = conn.SetWriteBuffer(networkBuffer); err != nil {
		Printf("udp server video conn set write buffer error, %v", err)
	}
	bufUDP := make([]byte, 1048576)
	Printf("udp server start listen video port[%d]", config.MediaPort)
	defer Printf("udp server stop listen video port[%d]", config.MediaPort)
	for n, _, err := conn.ReadFromUDP(bufUDP); err == nil; n, _, err = conn.ReadFromUDP(bufUDP) {
		ps := bufUDP[:n]
		if err := rtpPacket.Unmarshal(ps); err != nil {
			Println("gb28181 decode rtp error:", err)
		}
		if publisher := publishers.Get(rtpPacket.SSRC); publisher != nil && publisher.Err() == nil {
			publisher.PushPS(&rtpPacket)
		}
	}
}

func queryCatalog(config *transaction.Config) {
	t := time.NewTicker(time.Duration(config.CatalogInterval) * time.Second)
	for range t.C {
		Devices.Range(func(key, value interface{}) bool {
			device := value.(*Device)
			if time.Since(device.UpdateTime) > time.Duration(config.RegisterValidity)*time.Second {
				Devices.Delete(key)
			} else {
				go device.Query()
			}
			return true
		})
	}
}
