package main

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"flag"
	"fmt"
	"io/ioutil"
	"log"
	"os"
	"reflect"
	"strconv"
	"strings"
	"testing"
	"time"

	sds "github.com/Azure/sonic-telemetry/dialout/dialout_server"
	testcert "github.com/Azure/sonic-telemetry/testdata/tls"
	"github.com/golang/protobuf/proto"
	gclient "github.com/jipanyang/gnmi/client/gnmi"
	"github.com/kylelemons/godebug/pretty"
	"github.com/openconfig/gnmi/client"
	pb "github.com/openconfig/gnmi/proto/gnmi"
	"github.com/openconfig/gnmi/value"
	"golang.org/x/crypto/ssh"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/status"
)

var globalSSHClient *ssh.Client

var clientTypes = []string{gclient.Type}

var emptyRespVal = make(map[string]string)

var dialinAddr string
var dialoutAddr1 string
var dialoutAddr2 string

var portNameMap map[string]string
var queueNameMap map[string]string
var aliasNameMap map[string]string
var queueOfEthernet4 map[string]map[string]string

var ethernetWildcardByte []uint8
var ethernetWildcardPfc7Byte []uint8
var queueOfEthernet4Byte []uint8

// var sshSession *ssh.Session

var targetIP = flag.String("telemetryIP", "", "the IP of target")
var targetPort = flag.String("telemetryPort", "", "the port of target")
var dialoutIP = flag.String("dialoutIP", "", "the IP of dialout client")

// var targetIP = flag.String("targetIP", "", "the IP of target")

func TestMain(m *testing.M) {
	// cmd := exec.Command("export CVL_SCHEMA_PATH=/mnt/d/Code/sonic_buildimage_src/src/sonic-telemetry/debian/sonic-telemetry/usr/sbin/schema")
	// if _, err := cmd.Output(); err != nil {
	// 	fmt.Println(err)
	// 	os.Exit(1)
	// }
	// os.Setenv("CVL_SCHEMA_PATH", "/mnt/d/Code/sonic_buildimage_src/src/sonic-telemetry/debian/sonic-telemetry/usr/sbin/schema")
	flag.Parse()
	if *targetIP == "" || *targetPort == "" || *dialoutIP == "" {
		flag.Usage()
		return
	}
	sshHost := *targetIP
	sshPort := 22
	sshUser := "admin"
	sshPassword := "YourPaSsWoRd"
	config := &ssh.ClientConfig{
		Timeout:         time.Second,
		User:            sshUser,
		HostKeyCallback: ssh.InsecureIgnoreHostKey(),
		Auth:            []ssh.AuthMethod{ssh.Password(sshPassword)},
	}
	addr := fmt.Sprintf("%s:%d", sshHost, sshPort)
	var err error
	globalSSHClient, err = ssh.Dial("tcp", addr, config)
	if err != nil {
		log.Fatalf("Create ssh client failed: %v", err)
	}
	defer globalSSHClient.Close()

	dialinAddr = fmt.Sprintf("%s:%s", *targetIP, *targetPort)
	dialoutAddr1 = fmt.Sprintf("%s:%d", *dialoutIP, 8080)
	dialoutAddr2 = fmt.Sprintf("%s:%d", *dialoutIP, 8081)

	// exe_cmd(nil, "/usr/bin/telemetry.sh stop")
	// exe_cmd(nil, "/usr/bin/telemetry.sh start")

	fmt.Println("TestMain")
	os.Exit(m.Run())
}

func prepareData(t *testing.T, name string) {
	switch name {
	case "portNameMap":
		if portNameMap == nil {
			portNameMap = getPortNameMap(t)
		}
	case "queueNameMap":
		if queueNameMap == nil {
			queueNameMap = getQueueNameMap(t)
		}
	case "aliasNameMap":
		if aliasNameMap == nil {
			aliasNameMap = getAliasNameMap(t)
		}
	case "queueOfEthernet4":
		prepareData(t, "queueNameMap")
		if queueOfEthernet4 == nil {
			queueOfEthernet4 = getAllQueueFromPortName(t, "Ethernet4")
		}
	case "countersEthernetWildcardByte":
		prepareData(t, "aliasNameMap")
		prepareData(t, "portNameMap")
		if ethernetWildcardByte == nil {
			ethernetWildcardByte = getEthernetWildcard(t, aliasNameMap, portNameMap)
		}
	case "countersEthernetWildcardPfc7Byte":
		if ethernetWildcardPfc7Byte == nil {
			ethernetWildcardPfc7Byte = getEthernetWildcardPfcByte(t, "SAI_PORT_STAT_PFC_7_RX_PKTS")
		}
	}
}

func removeTestDataFromPortNameMap(t *testing.T) {
	hdelCountersDB(t, "COUNTERS_PORT_NAME_MAP", "test_field")
	time.Sleep(time.Millisecond * 500)
}

func removeTestDataFromSinglePort(t *testing.T, oid string) {
	hdelCountersDB(t, "COUNTERS:"+oid, "test_field")
	time.Sleep(time.Millisecond * 500)
}

func getSSHSession(t *testing.T) *ssh.Session {
	session, err := globalSSHClient.NewSession()
	if err != nil {
		t.Fatalf("Create ssh session failed, %v", err)
	}
	return session
}

func sshHgetall(t *testing.T, session *ssh.Session, cmd string) map[string]string {
	var result map[string]string = make(map[string]string)
	out, err := session.Output(cmd)
	if err != nil {
		t.Fatalf("Hgetall data from Redis by SSH failed: %v", err)
	}
	splited := strings.Split(strings.Trim(string(out), "\n"), ",")
	var mapKey, value string
	if len(splited) > 1 && len(splited)%2 == 0 {
		for i := 0; i < len(splited); i = i + 2 {
			mapKey, _ = strconv.Unquote(splited[i])
			value, _ = strconv.Unquote(splited[i+1])
			result[mapKey] = value
		}
	}
	return result
}

func sshHget(t *testing.T, session *ssh.Session, cmd string) string {
	out, err := session.Output(cmd)
	if err != nil {
		t.Fatalf("Hget data from Redis by SSH failed: %v", err)
	}
	// trim \" and newline
	return strings.Trim(string(out), "\n\"")
}

func sshHset(t *testing.T, session *ssh.Session, cmd string) string {
	out, err := session.Output(cmd)
	if err != nil {
		t.Fatalf("Hset data to Redis by SSH failed: %v", err)
	}
	// trim \" and newline
	return strings.Trim(string(out), "\n\"")
}

func sshRedisCli(t *testing.T, session *ssh.Session, cmd string) string {
	// t.Log(cmd)
	out, err := session.Output(cmd)
	if err != nil {
		t.Fatalf("Operate to Redis by SSH failed: %v", err)
	}
	// trim \" and newline
	return strings.Trim(string(out), "\n\"")
}

func getFieldDataOfVirtualPath(t *testing.T, portName string, field string, portNameMap map[string]string) string {
	key := "COUNTERS:" + portNameMap[portName]
	cmd := fmt.Sprintf("redis-cli -n 2 --csv hget %s %s", key, field)

	sshSession := getSSHSession(t)
	defer sshSession.Close()
	result := sshHget(t, sshSession, cmd)
	return result
}

func getDataOfVirtualPath(t *testing.T, portName string, portNameMap map[string]string) []uint8 {
	redisKey := "COUNTERS:" + portNameMap[portName]
	cmd := fmt.Sprintf("redis-cli -n 2 --csv hgetall %s", redisKey)

	sshSession := getSSHSession(t)
	defer sshSession.Close()

	result := sshHgetall(t, sshSession, cmd)
	jsonBytes, _ := json.Marshal(result)
	return jsonBytes
}

func getPortNameMap(t *testing.T) map[string]string {
	var portNameMap map[string]string
	redisKey := "COUNTERS_PORT_NAME_MAP"
	cmd := fmt.Sprintf("redis-cli -n 2 --csv hgetall %s", redisKey)

	sshSession := getSSHSession(t)
	defer sshSession.Close()

	portNameMap = sshHgetall(t, sshSession, cmd)
	return portNameMap
}

func getQueueNameMap(t *testing.T) map[string]string {
	var portNameMap map[string]string
	redisKey := "COUNTERS_QUEUE_NAME_MAP"
	cmd := fmt.Sprintf("redis-cli -n 2 --csv hgetall %s", redisKey)

	sshSession := getSSHSession(t)
	defer sshSession.Close()

	portNameMap = sshHgetall(t, sshSession, cmd)
	return portNameMap
}

func getAliasNameMap(t *testing.T) map[string]string {
	var aliasNameMap map[string]string = make(map[string]string)
	key := "\"PORT|*\""
	cmd := fmt.Sprintf("redis-cli -n 4 --csv keys %s", key)

	sshSession := getSSHSession(t)
	defer sshSession.Close()

	out, err := sshSession.Output(cmd)
	if err != nil {
		t.Fatalf("keys from Redis by SSH failed: %v", err)
	}
	splited := strings.Split(strings.Trim(string(out), "\n"), ",")

	// cmd = ""
	// for _, item := range splited {
	// 	tmpCmd := fmt.Sprintf("redis-cli -n 4 --csv hget %s %s && ", item, "alias")
	// 	cmd += tmpCmd
	// }
	// fmt.Println(cmd)
	// cmd = ""
	// for _, item := range splited {
	// 	tmpCmd := fmt.Sprintf("redis-cli -n 4 hget %s %s && ", item, "alias")
	// 	cmd += tmpCmd
	// }
	// fmt.Println(cmd)
	// cmd = "( "
	// for _, item := range splited {
	// 	tmpCmd := fmt.Sprintf("redis-cli -n 4 --csv hget %s %s ; ", item, "alias")
	// 	cmd += tmpCmd
	// }
	// fmt.Println(cmd)
	// cmd = "rm result.log && "
	// for _, item := range splited {
	// 	tmpCmd := fmt.Sprintf("redis-cli -n 4 --csv hget %s %s >> result.log & ", item, "alias")
	// 	cmd += tmpCmd
	// }
	// cmd = cmd + "wait"
	// for range splited {
	// 	cmd += " && fg"
	// }
	// fmt.Println(cmd)
	// tmpSession := getSSHSession(t)
	// out2, err := tmpSession.Output("pwd")
	// if err != nil {
	// 	t.Fatalf("Hgetall data from Redis by SSH failed: %v", err)
	// }
	// fmt.Println("pwd", string(out2))

	for _, item := range splited {
		//PORT|EthernetXX
		portKey := strings.Trim(item, "\"")[5:]
		tmpSession := getSSHSession(t)
		tmpCmd := fmt.Sprintf("redis-cli -n 4 --csv hget %s %s", item, "alias")
		// fmt.Printf("[Preparing compared data] Getting alias of %v now", portKey)
		alias := sshHget(t, tmpSession, tmpCmd)
		// fmt.Printf("\r[Preparing compared data] Getting alias of %v -> %v\n", portKey, alias)
		aliasNameMap[alias] = portKey
		tmpSession.Close()
	}

	return aliasNameMap
}

func getEthernetWildcard(t *testing.T, aliasNameMap, portNameMap map[string]string) []uint8 {
	var result map[string]map[string]string = make(map[string]map[string]string)
	// fmt.Println("aliasNameMap", len(aliasNameMap), aliasNameMap)
	// fmt.Println("portNameMap", len(portNameMap), portNameMap)
	for alias, portName := range aliasNameMap {
		redisKey := "COUNTERS:" + portNameMap[portName]
		cmd := fmt.Sprintf("redis-cli -n 2 --csv hgetall %s", redisKey)

		sshSession := getSSHSession(t)

		// fmt.Printf("[Preparing compared data] Getting data of %v now\n", alias)
		tmpResult := sshHgetall(t, sshSession, cmd)

		result[alias] = tmpResult
		// fmt.Println(alias, cmd)
		sshSession.Close()
	}
	jsonBytes, _ := json.Marshal(result)
	// fmt.Println(result)
	return jsonBytes
}

func TestGnmiGet(t *testing.T) {
	removeTestDataFromPortNameMap(t)
	prepareData(t, "portNameMap")
	prepareData(t, "aliasNameMap")
	prepareData(t, "queueNameMap")

	countersPortNameMapByte, _ := json.Marshal(portNameMap)
	countersEthernet4Byte := getDataOfVirtualPath(t, "Ethernet4", portNameMap)
	if ethernetWildcardByte == nil {
		ethernetWildcardByte = getEthernetWildcard(t, aliasNameMap, portNameMap)
	}
	countersEthernet4Pfc7String := getFieldDataOfVirtualPath(t, "Ethernet4", "SAI_PORT_STAT_PFC_7_RX_PKTS", portNameMap)
	prepareData(t, "queueOfEthernet4")
	if queueOfEthernet4Byte == nil {
		queueOfEthernet4Byte, _ = json.Marshal(queueOfEthernet4)
	}
	oid4, ok := portNameMap["Ethernet4"]
	if !ok {
		t.Fatalf("[TestGNMIGet] cannot find the oid of Ethernet4")
	} else {
		t.Logf("Ethernet4=%v", oid4)
	}

	// t.Log("Start gNMI client")
	tlsConfig := &tls.Config{InsecureSkipVerify: true}
	opts := []grpc.DialOption{grpc.WithTransportCredentials(credentials.NewTLS(tlsConfig))}

	// targetAddr := "192.168.3.20:8080"
	targetAddr := dialinAddr
	conn, err := grpc.Dial(targetAddr, opts...)
	if err != nil {
		t.Fatalf("Dialing to %q failed: %v", targetAddr, err)
	}
	defer conn.Close()

	gClient := pb.NewGNMIClient(conn)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	tds := []struct {
		desc        string
		pathTarget  string
		textPbPath  string
		wantRetCode codes.Code
		wantRespVal interface{}
		valTest     bool
	}{
		{
			desc:       "Test non-existing path Target",
			pathTarget: "MY_DB",
			textPbPath: `
					elem: <name: "MyCounters" >
				`,
			wantRetCode: codes.NotFound,
		}, {
			desc:       "Test empty path target",
			pathTarget: "",
			textPbPath: `
					elem: <name: "MyCounters" >
				`,
			wantRetCode: codes.Unimplemented,
		}, {
			desc:       "Get valid but non-existing node",
			pathTarget: "COUNTERS_DB",
			textPbPath: `
					elem: <name: "MyCounters" >
				`,
			wantRetCode: codes.NotFound,
		}, {
			desc:       "[RealPath]Get COUNTERS_PORT_NAME_MAP",
			pathTarget: "COUNTERS_DB",
			textPbPath: `
					elem: <name: "COUNTERS_PORT_NAME_MAP" >
				`,
			wantRetCode: codes.OK,
			wantRespVal: countersPortNameMapByte,
			valTest:     true,
		}, {
			desc:       "[RealPath]Get oid of Ethernet4 from COUNTERS_PORT_NAME_MAP",
			pathTarget: "COUNTERS_DB",
			textPbPath: `
					elem: <name: "COUNTERS_PORT_NAME_MAP" >
					elem: <name: "Ethernet4" >
				`,
			wantRetCode: codes.OK,
			wantRespVal: oid4,
			valTest:     true,
		}, {
			desc:       "[VirtualPath]Get COUNTERS_Ethernet4",
			pathTarget: "COUNTERS_DB",
			textPbPath: `
							elem: <name: "COUNTERS" >
							elem: <name: "Ethernet4" >
						`,
			wantRetCode: codes.OK,
			wantRespVal: countersEthernet4Byte,
			valTest:     false,
		}, {
			desc:       "[VirtualPath]Get COUNTERS_Ethernet4 SAI_PORT_STAT_PFC_7_RX_PKTS",
			pathTarget: "COUNTERS_DB",
			textPbPath: `
						elem: <name: "COUNTERS" >
						elem: <name: "Ethernet4" >
						elem: <name: "SAI_PORT_STAT_PFC_7_RX_PKTS" >
					`,
			wantRetCode: codes.OK,
			wantRespVal: countersEthernet4Pfc7String,
			valTest:     true,
		}, {
			desc:       "[VirtualPath*]Get COUNTERS_Ethernet*",
			pathTarget: "COUNTERS_DB",
			textPbPath: `
								elem: <name: "COUNTERS" >
								elem: <name: "Ethernet*" >
							`,
			wantRetCode: codes.OK,
			wantRespVal: ethernetWildcardByte,
			valTest:     false,
		}, {
			desc:       "[VirtualPath]Get COUNTERS_Ethernet4 Queues",
			pathTarget: "COUNTERS_DB",
			textPbPath: `
								elem: <name: "COUNTERS" >
								elem: <name: "Ethernet4" >
								elem: <name: "Queues" >
							`,
			wantRetCode: codes.OK,
			wantRespVal: queueOfEthernet4Byte,
			valTest:     false,
		}, {
			desc:       "[Non-DB]Get platform cpu",
			pathTarget: "OTHERS",
			textPbPath: `
								elem: <name: "platform" >
								elem: <name: "cpu" >
							`,
			wantRetCode: codes.OK,
			wantRespVal: emptyRespVal,
			valTest:     false,
		},
	}
	for _, td := range tds {
		t.Run(td.desc, func(t *testing.T) {
			runTestGet(t, ctx, gClient, td.pathTarget, td.textPbPath, td.wantRetCode, td.wantRespVal, td.valTest)
		})
	}

}

func TestGnmiGetTranslib(t *testing.T) {
	tlsConfig := &tls.Config{InsecureSkipVerify: true}
	opts := []grpc.DialOption{grpc.WithTransportCredentials(credentials.NewTLS(tlsConfig))}

	targetAddr := dialinAddr
	conn, err := grpc.Dial(targetAddr, opts...)
	if err != nil {
		t.Fatalf("Dialing to %q failed: %v", targetAddr, err)
	}
	defer conn.Close()

	gClient := pb.NewGNMIClient(conn)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	var emptyRespVal interface{}
	tds := []struct {
		desc        string
		pathTarget  string
		textPbPath  string
		wantRetCode codes.Code
		wantRespVal interface{}
		valTest     bool
	}{
		{
			desc:       "Get OC Interfaces",
			pathTarget: "OC_YANG",
			textPbPath: `
		                elem: <name: "openconfig-interfaces:interfaces" >
		        `,
			wantRetCode: codes.OK,
			wantRespVal: emptyRespVal,
			valTest:     false,
		}, {
			desc:       "Get OC Interface Ethernet4",
			pathTarget: "OC_YANG",
			textPbPath: `
		                elem: <name: "openconfig-interfaces:interfaces" > elem: <name: "interface" key:<key:"name" value:"Ethernet4" > >
		        `,
			wantRetCode: codes.OK,
			wantRespVal: emptyRespVal,
			valTest:     false,
		}, {
			desc:       "Get OC Interface Ethernet4 admin-status",
			pathTarget: "OC_YANG",
			textPbPath: `
		                elem: <name: "openconfig-interfaces:interfaces" > elem: <name: "interface" key:<key:"name" value:"Ethernet4" > > elem: <name: "state" > elem: <name: "admin-status" >
		        `,
			wantRetCode: codes.OK,
			wantRespVal: emptyRespVal,
			valTest:     false,
		}, {
			desc:       "Get OC Interface Ethernet4 mtu",
			pathTarget: "OC_YANG",
			textPbPath: `
		                elem: <name: "openconfig-interfaces:interfaces" > elem: <name: "interface" key:<key:"name" value:"Ethernet4" > > elem: <name: "state" > elem: <name: "mtu" >
		        `,
			wantRetCode: codes.OK,
			wantRespVal: emptyRespVal,
			valTest:     false,
		},
	}
	for _, td := range tds {
		t.Run(td.desc, func(t *testing.T) {
			runTestGet(t, ctx, gClient, td.pathTarget, td.textPbPath, td.wantRetCode, td.wantRespVal, td.valTest)
		})
	}
}

func TestCapabilities(t *testing.T) {
	//t.Log("Start gNMI client")
	tlsConfig := &tls.Config{InsecureSkipVerify: true}
	opts := []grpc.DialOption{grpc.WithTransportCredentials(credentials.NewTLS(tlsConfig))}

	//targetAddr := "30.57.185.38:8080"
	// targetAddr := "192.168.3.20:8080"
	targetAddr := dialinAddr
	conn, err := grpc.Dial(targetAddr, opts...)
	if err != nil {
		t.Fatalf("Dialing to %q failed: %v", targetAddr, err)
	}
	defer conn.Close()

	gClient := pb.NewGNMIClient(conn)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	var req pb.CapabilityRequest
	resp, err := gClient.Capabilities(ctx, &req)
	if err != nil {
		t.Fatalf("Failed to get Capabilities")
	}
	if len(resp.SupportedModels) == 0 {
		t.Fatalf("No Supported Models found!")
	}
}

// runTestGet requests a path from the server by Get grpc call, and compares if
// the return code and response value are expected.
func runTestGet(t *testing.T, ctx context.Context, gClient pb.GNMIClient, pathTarget string,
	textPbPath string, wantRetCode codes.Code, wantRespVal interface{}, valTest bool) {
	//var retCodeOk bool
	// Send request

	var pbPath pb.Path
	if err := proto.UnmarshalText(textPbPath, &pbPath); err != nil {
		t.Fatalf("error in unmarshaling path: %v %v", textPbPath, err)
	}
	prefix := pb.Path{Target: pathTarget}
	req := &pb.GetRequest{
		Prefix:   &prefix,
		Path:     []*pb.Path{&pbPath},
		Encoding: pb.Encoding_JSON_IETF,
	}

	resp, err := gClient.Get(ctx, req)
	// Check return code
	gotRetStatus, ok := status.FromError(err)
	if !ok {
		t.Fatal("got a non-grpc error from grpc call")
	}

	if gotRetStatus.Code() != wantRetCode {
		t.Log("err: ", err)
		t.Fatalf("got return code %v, want %v", gotRetStatus.Code(), wantRetCode)
	}

	// Check response value
	if valTest {
		var gotVal interface{}
		if resp != nil {
			notifs := resp.GetNotification()
			if len(notifs) != 1 {
				t.Fatalf("got %d notifications, want 1", len(notifs))
			}
			updates := notifs[0].GetUpdate()
			if len(updates) != 1 {
				t.Fatalf("got %d updates in the notification, want 1", len(updates))
			}
			val := updates[0].GetVal()
			if val.GetJsonIetfVal() == nil {
				gotVal, err = value.ToScalar(val)
				if err != nil {
					t.Errorf("got: %v, want a scalar value", gotVal)
				}
			} else {
				// Unmarshal json data to gotVal container for comparison
				if err := json.Unmarshal(val.GetJsonIetfVal(), &gotVal); err != nil {
					t.Fatalf("error in unmarshaling IETF JSON data to json container: %v", err)
				}
				// fmt.Println(string(val.GetJsonIetfVal()))
				var wantJSONStruct interface{}
				if err := json.Unmarshal(wantRespVal.([]byte), &wantJSONStruct); err != nil {
					t.Fatalf("error in unmarshaling IETF JSON data to json container: %v", err)
				}
				wantRespVal = wantJSONStruct
			}
		}

		if !reflect.DeepEqual(gotVal, wantRespVal) {
			t.Errorf("got: %v (%T),\nwant %v (%T)", gotVal, gotVal, wantRespVal, wantRespVal)
		}
	}
}

type tablePathValue struct {
	dbName    string
	tableName string
	tableKey  string
	delimitor string
	field     string
	value     string
	op        string
}

func copyMap(originalMap map[string]string) map[string]string {
	newMap := make(map[string]string)
	for k, v := range originalMap {
		newMap[k] = v
	}
	return newMap
}

func getAllQueueFromPortName(t *testing.T, port string) map[string]map[string]string {
	result := make(map[string]map[string]string)
	for que, oid := range queueNameMap {
		//que is in format of "Ethernet64:12"
		names := strings.Split(que, ":")
		if port != names[0] {
			continue
		}
		key := "COUNTERS:" + oid
		cmd := fmt.Sprintf("redis-cli -n 2 --csv hgetall %s", key)

		sshSession := getSSHSession(t)

		// fmt.Printf("[Preparing compared data] Getting data of %v now\n", que)
		tmpResult := sshHgetall(t, sshSession, cmd)

		result[que] = tmpResult

		sshSession.Close()
	}
	return result
}

func runTestSubscribe(t *testing.T) {

	removeTestDataFromPortNameMap(t)
	prepareData(t, "portNameMap")
	prepareData(t, "queueNameMap")

	portNameMapUpdate := copyMap(portNameMap)
	portNameMapUpdate["test_field"] = "test_value"

	oid4, ok := portNameMap["Ethernet4"]
	if !ok {
		t.Fatalf("[TestSubscribe] cannot find the oid of Ethernet4")
	} else {
		t.Logf("Ethernet4=%v", oid4)
	}

	removeTestDataFromSinglePort(t, oid4)

	var countersEthernet4Json interface{}
	countersEthernet4Byte := getDataOfVirtualPath(t, "Ethernet4", portNameMap)
	json.Unmarshal(countersEthernet4Byte, &countersEthernet4Json)
	var tmp interface{}
	json.Unmarshal(countersEthernet4Byte, &tmp)
	countersEthernet4JsonUpdate := tmp.(map[string]interface{})
	countersEthernet4JsonUpdate["test_field"] = "test_value"

	// prepare for test 3: stream query for COUNTERS/Ethernet4/SAI_PORT_STAT_PFC_7_RX_PKTS with update of field value
	countersEthernet4Pfc7String := getFieldDataOfVirtualPath(t, "Ethernet4", "SAI_PORT_STAT_PFC_7_RX_PKTS", portNameMap)

	// prepare for queue test
	prepareData(t, "queueOfEthernet4")

	tests := []struct {
		desc       string
		q          client.Query
		prepares   []tablePathValue
		updates    []tablePathValue
		recoveries []tablePathValue
		wantErr    bool
		wantNoti   []client.Notification

		poll        int
		wantPollErr string
		trimResult  bool
		compareData bool
	}{
		// stream mode
		{
			desc: "[stream][RealPath]stream query for table COUNTERS_PORT_NAME_MAP with new test_field field",
			q: client.Query{
				Target:  "COUNTERS_DB",
				Type:    client.Stream,
				Queries: []client.Path{{"COUNTERS_PORT_NAME_MAP"}},
				TLS:     &tls.Config{InsecureSkipVerify: true},
			},
			prepares: []tablePathValue{{
				dbName:    "COUNTERS_DB",
				tableName: "COUNTERS_PORT_NAME_MAP",
				field:     "test_field",
				op:        "hdel",
			}},
			updates: []tablePathValue{{
				dbName:    "COUNTERS_DB",
				tableName: "COUNTERS_PORT_NAME_MAP",
				field:     "test_field",
				value:     "test_value",
			}},
			wantNoti: []client.Notification{
				client.Connected{},
				client.Update{Path: []string{"COUNTERS_PORT_NAME_MAP"}, TS: time.Unix(0, 200), Val: portNameMap},
				client.Sync{},
				client.Update{Path: []string{"COUNTERS_PORT_NAME_MAP"}, TS: time.Unix(0, 200), Val: portNameMapUpdate},
			},
			recoveries: []tablePathValue{{
				dbName:    "COUNTERS_DB",
				tableName: "COUNTERS_PORT_NAME_MAP",
				field:     "test_field",
				op:        "hdel",
			}},
			compareData: true,
		},
		{
			desc: "[stream][VirtualPath]stream query for table key Ethernet4 with new test_field field",
			q: client.Query{
				Target:  "COUNTERS_DB",
				Type:    client.Stream,
				Queries: []client.Path{{"COUNTERS", "Ethernet4"}},
				TLS:     &tls.Config{InsecureSkipVerify: true},
			},
			prepares: []tablePathValue{{
				dbName:    "COUNTERS_DB",
				tableName: "COUNTERS",
				tableKey:  oid4,
				delimitor: ":",
				field:     "test_field",
				op:        "hdel",
			}},
			updates: []tablePathValue{{
				dbName:    "COUNTERS_DB",
				tableName: "COUNTERS",
				tableKey:  oid4,
				delimitor: ":",
				field:     "test_field",
				value:     "test_value",
			}, { //Same value set should not trigger multiple updates
				dbName:    "COUNTERS_DB",
				tableName: "COUNTERS",
				tableKey:  oid4,
				delimitor: ":",
				field:     "test_field",
				value:     "test_value",
			}},
			wantNoti: []client.Notification{
				client.Connected{},
				client.Update{Path: []string{"COUNTERS", "Ethernet4"}, TS: time.Unix(0, 200), Val: countersEthernet4Json},
				client.Sync{},
				client.Update{Path: []string{"COUNTERS", "Ethernet4"}, TS: time.Unix(0, 200), Val: countersEthernet4JsonUpdate},
			},
			recoveries: []tablePathValue{{
				dbName:    "COUNTERS_DB",
				tableName: "COUNTERS",
				tableKey:  oid4,
				delimitor: ":",
				field:     "test_field",
				op:        "hdel",
			}},
			// Since the conter value may be set before the update applies, therefore it may get the response which is not include the update value.
			// compareData: true,
			trimResult: true,
		}, {
			// It will be changed back by the switch, therefore the value for comparing may not be correct.
			desc: "[stream][VirtualPath]stream query for COUNTERS/Ethernet4/SAI_PORT_STAT_PFC_7_RX_PKTS with update of field value",
			q: client.Query{
				Target:  "COUNTERS_DB",
				Type:    client.Stream,
				Queries: []client.Path{{"COUNTERS", "Ethernet4", "SAI_PORT_STAT_PFC_7_RX_PKTS"}},
				TLS:     &tls.Config{InsecureSkipVerify: true},
			},
			updates: []tablePathValue{{
				dbName:    "COUNTERS_DB",
				tableName: "COUNTERS",
				tableKey:  oid4, // "Ethernet4": "oid_0x1000000000039",
				delimitor: ":",
				field:     "SAI_PORT_STAT_PFC_7_RX_PKTS",
				value:     "1", // be changed to 1 from 0
			}},
			wantNoti: []client.Notification{
				client.Connected{},
				client.Update{Path: []string{"COUNTERS", "Ethernet4", "SAI_PORT_STAT_PFC_7_RX_PKTS"}, TS: time.Unix(0, 200), Val: emptyRespVal},
				client.Sync{},
				client.Update{Path: []string{"COUNTERS", "Ethernet4", "SAI_PORT_STAT_PFC_7_RX_PKTS"}, TS: time.Unix(0, 200), Val: emptyRespVal},
			},
			trimResult: true,
		}, {
			// In the real switch, the counters in ports will be change any time, therefore the value for comparing may not be correct.
			desc: "[stream][VirtualPath*]stream query for table key Ethernet* with new test_field field on Ethernet4",
			q: client.Query{
				Target:  "COUNTERS_DB",
				Type:    client.Stream,
				Queries: []client.Path{{"COUNTERS", "Ethernet*"}},
				TLS:     &tls.Config{InsecureSkipVerify: true},
			},
			prepares: []tablePathValue{{
				dbName:    "COUNTERS_DB",
				tableName: "COUNTERS",
				tableKey:  oid4,
				delimitor: ":",
				field:     "test_field",
				op:        "hdel",
			}},
			updates: []tablePathValue{{
				dbName:    "COUNTERS_DB",
				tableName: "COUNTERS",
				tableKey:  oid4, // "Ethernet4": "oid_0x1000000000039",
				delimitor: ":",
				field:     "test_field",
				value:     "test_value",
			}, { //Same value set should not trigger multiple updates
				dbName:    "COUNTERS_DB",
				tableName: "COUNTERS",
				tableKey:  oid4, // "Ethernet4": "oid_0x1000000000039",
				delimitor: ":",
				field:     "test_field",
				value:     "test_value",
			}},
			wantNoti: []client.Notification{
				client.Connected{},
				client.Update{Path: []string{"COUNTERS", "Ethernet*"},
					TS: time.Unix(0, 200), Val: emptyRespVal},
				client.Sync{},
				client.Update{Path: []string{"COUNTERS", "Ethernet*"},
					TS: time.Unix(0, 200), Val: emptyRespVal},
			},
			recoveries: []tablePathValue{{
				dbName:    "COUNTERS_DB",
				tableName: "COUNTERS",
				tableKey:  oid4,
				delimitor: ":",
				field:     "test_field",
				op:        "hdel",
			}},
			trimResult: true,
		}, {
			desc: "[stream][VirtualPath*]stream query for table key Ethernet*/SAI_PORT_STAT_PFC_7_RX_PKTS with field value update",
			q: client.Query{
				Target:  "COUNTERS_DB",
				Type:    client.Stream,
				Queries: []client.Path{{"COUNTERS", "Ethernet*", "SAI_PORT_STAT_PFC_7_RX_PKTS"}},
				TLS:     &tls.Config{InsecureSkipVerify: true},
			},
			updates: []tablePathValue{
				{
					dbName:    "COUNTERS_DB",
					tableName: "COUNTERS",
					tableKey:  oid4, // "Ethernet4": "oid_0x1000000000039",
					delimitor: ":",
					field:     "SAI_PORT_STAT_PFC_7_RX_PKTS",
					value:     "4", // being changed to 4 from 0
				},
				{
					dbName:    "COUNTERS_DB",
					tableName: "COUNTERS",
					tableKey:  oid4, // "Ethernet4": "oid_0x1000000000039",
					delimitor: ":",
					field:     "SAI_PORT_STAT_PFC_7_RX_PKTS",
					value:     "3", // being changed to 3 from 4
				},
			},
			wantNoti: []client.Notification{
				client.Connected{},
				client.Update{Path: []string{"COUNTERS", "Ethernet*", "SAI_PORT_STAT_PFC_7_RX_PKTS"},
					TS: time.Unix(0, 200), Val: emptyRespVal},
				client.Sync{},
				client.Update{Path: []string{"COUNTERS", "Ethernet*", "SAI_PORT_STAT_PFC_7_RX_PKTS"},
					TS: time.Unix(0, 200), Val: emptyRespVal},
			},
			trimResult: true,
		},
		// poll mode
		{
			desc: "[poll][RealPath]poll query for table COUNTERS_PORT_NAME_MAP with new field test_field",
			poll: 3,
			q: client.Query{
				Target:  "COUNTERS_DB",
				Type:    client.Poll,
				Queries: []client.Path{{"COUNTERS_PORT_NAME_MAP"}},
				TLS:     &tls.Config{InsecureSkipVerify: true},
			},
			prepares: []tablePathValue{{
				dbName:    "COUNTERS_DB",
				tableName: "COUNTERS_PORT_NAME_MAP",
				field:     "test_field",
				op:        "hdel",
			}},
			updates: []tablePathValue{{
				dbName:    "COUNTERS_DB",
				tableName: "COUNTERS_PORT_NAME_MAP",
				field:     "test_field",
				value:     "test_value",
			}},
			wantNoti: []client.Notification{
				client.Connected{},
				// We are starting from the result data of "stream query for table with update of new field",
				client.Update{Path: []string{"COUNTERS_PORT_NAME_MAP"}, TS: time.Unix(0, 200), Val: portNameMap},
				client.Sync{},
				client.Update{Path: []string{"COUNTERS_PORT_NAME_MAP"}, TS: time.Unix(0, 200), Val: portNameMapUpdate},
				client.Sync{},
				client.Update{Path: []string{"COUNTERS_PORT_NAME_MAP"}, TS: time.Unix(0, 200), Val: portNameMapUpdate},
				client.Sync{},
				client.Update{Path: []string{"COUNTERS_PORT_NAME_MAP"}, TS: time.Unix(0, 200), Val: portNameMapUpdate},
				client.Sync{},
			},
			recoveries: []tablePathValue{{
				dbName:    "COUNTERS_DB",
				tableName: "COUNTERS_PORT_NAME_MAP",
				field:     "test_field",
				op:        "hdel",
			}},
			compareData: true,
		},
		{
			desc: "[poll][RealPath]poll query for table COUNTERS_PORT_NAME_MAP with test_field delete",
			poll: 3,
			q: client.Query{
				Target:  "COUNTERS_DB",
				Type:    client.Poll,
				Queries: []client.Path{{"COUNTERS_PORT_NAME_MAP"}},
				TLS:     &tls.Config{InsecureSkipVerify: true},
			},
			prepares: []tablePathValue{{
				dbName:    "COUNTERS_DB",
				tableName: "COUNTERS_PORT_NAME_MAP",
				field:     "test_field",
				value:     "test_value",
			}},
			updates: []tablePathValue{{
				dbName:    "COUNTERS_DB",
				tableName: "COUNTERS_PORT_NAME_MAP",
				field:     "test_field",
				op:        "hdel",
			}},
			wantNoti: []client.Notification{
				client.Connected{},
				// We are starting from the result data of "stream query for table with update of new field",
				client.Update{Path: []string{"COUNTERS_PORT_NAME_MAP"}, TS: time.Unix(0, 200), Val: portNameMapUpdate},
				client.Sync{},
				client.Update{Path: []string{"COUNTERS_PORT_NAME_MAP"}, TS: time.Unix(0, 200), Val: portNameMap},
				client.Sync{},
				client.Update{Path: []string{"COUNTERS_PORT_NAME_MAP"}, TS: time.Unix(0, 200), Val: portNameMap},
				client.Sync{},
				client.Update{Path: []string{"COUNTERS_PORT_NAME_MAP"}, TS: time.Unix(0, 200), Val: portNameMap},
				client.Sync{},
			},
			compareData: true,
		},
		{
			desc: "[poll][VirtualPath]poll query for COUNTERS/Ethernet4/SAI_PORT_STAT_PFC_7_RX_PKTS with field value change",
			poll: 3,
			q: client.Query{
				Target:  "COUNTERS_DB",
				Type:    client.Poll,
				Queries: []client.Path{{"COUNTERS", "Ethernet4", "SAI_PORT_STAT_PFC_7_RX_PKTS"}},
				TLS:     &tls.Config{InsecureSkipVerify: true},
			},
			updates: []tablePathValue{{
				dbName:    "COUNTERS_DB",
				tableName: "COUNTERS",
				tableKey:  oid4, // "Ethernet4": "oid_0x1000000000039",
				delimitor: ":",
				field:     "SAI_PORT_STAT_PFC_7_RX_PKTS",
				value:     "4", // being changed to 4 from 2
			}},
			wantNoti: []client.Notification{
				client.Connected{},
				client.Update{Path: []string{"COUNTERS", "Ethernet4", "SAI_PORT_STAT_PFC_7_RX_PKTS"},
					TS: time.Unix(0, 200), Val: countersEthernet4Pfc7String},
				client.Sync{},
				client.Update{Path: []string{"COUNTERS", "Ethernet4", "SAI_PORT_STAT_PFC_7_RX_PKTS"},
					TS: time.Unix(0, 200), Val: countersEthernet4Pfc7String},
				client.Sync{},
				client.Update{Path: []string{"COUNTERS", "Ethernet4", "SAI_PORT_STAT_PFC_7_RX_PKTS"},
					TS: time.Unix(0, 200), Val: countersEthernet4Pfc7String},
				client.Sync{},
				client.Update{Path: []string{"COUNTERS", "Ethernet4", "SAI_PORT_STAT_PFC_7_RX_PKTS"},
					TS: time.Unix(0, 200), Val: countersEthernet4Pfc7String},
				client.Sync{},
			},
		},
		{
			desc: "[poll][VirtualPath*]poll query for table key Ethernet* with Ethernet4/SAI_PORT_STAT_PFC_7_RX_PKTS field value change",
			poll: 3,
			q: client.Query{
				Target:  "COUNTERS_DB",
				Type:    client.Poll,
				Queries: []client.Path{{"COUNTERS", "Ethernet*"}},
				TLS:     &tls.Config{InsecureSkipVerify: true},
			},
			updates: []tablePathValue{{
				dbName:    "COUNTERS_DB",
				tableName: "COUNTERS",
				tableKey:  oid4, // "Ethernet4": "oid_0x1000000000039",
				delimitor: ":",
				field:     "SAI_PORT_STAT_PFC_7_RX_PKTS",
				value:     "4", // being changed to 4 from 2
			}},
			wantNoti: []client.Notification{
				client.Connected{},
				client.Update{Path: []string{"COUNTERS", "Ethernet*"},
					TS: time.Unix(0, 200), Val: emptyRespVal},
				client.Sync{},
				client.Update{Path: []string{"COUNTERS", "Ethernet*"},
					TS: time.Unix(0, 200), Val: emptyRespVal},
				client.Sync{},
				client.Update{Path: []string{"COUNTERS", "Ethernet*"},
					TS: time.Unix(0, 200), Val: emptyRespVal},
				client.Sync{},
				client.Update{Path: []string{"COUNTERS", "Ethernet*"},
					TS: time.Unix(0, 200), Val: emptyRespVal},
				client.Sync{},
			},
		},
		{
			desc: "[poll][VirtualPath*]poll query for table key field Ethernet*/SAI_PORT_STAT_PFC_7_RX_PKTS with Ethernet68/SAI_PORT_STAT_PFC_7_RX_PKTS field value change",
			poll: 3,
			q: client.Query{
				Target:  "COUNTERS_DB",
				Type:    client.Poll,
				Queries: []client.Path{{"COUNTERS", "Ethernet*", "SAI_PORT_STAT_PFC_7_RX_PKTS"}},
				TLS:     &tls.Config{InsecureSkipVerify: true},
			},
			updates: []tablePathValue{{
				dbName:    "COUNTERS_DB",
				tableName: "COUNTERS",
				tableKey:  oid4, // "Ethernet4": "oid_0x1000000000039",
				delimitor: ":",
				field:     "SAI_PORT_STAT_PFC_7_RX_PKTS",
				value:     "4", // being changed to 4 from 2
			}},
			wantNoti: []client.Notification{
				client.Connected{},
				client.Update{Path: []string{"COUNTERS", "Ethernet*", "SAI_PORT_STAT_PFC_7_RX_PKTS"},
					TS: time.Unix(0, 200), Val: emptyRespVal},
				client.Sync{},
				client.Update{Path: []string{"COUNTERS", "Ethernet*", "SAI_PORT_STAT_PFC_7_RX_PKTS"},
					TS: time.Unix(0, 200), Val: emptyRespVal},
				client.Sync{},
				client.Update{Path: []string{"COUNTERS", "Ethernet*", "SAI_PORT_STAT_PFC_7_RX_PKTS"},
					TS: time.Unix(0, 200), Val: emptyRespVal},
				client.Sync{},
				client.Update{Path: []string{"COUNTERS", "Ethernet*", "SAI_PORT_STAT_PFC_7_RX_PKTS"},
					TS: time.Unix(0, 200), Val: emptyRespVal},
				client.Sync{},
			},
		},
		{
			desc: "[poll][VirtualPath*]poll query for COUNTERS/Ethernet*/Queues",
			poll: 1,
			q: client.Query{
				Target:  "COUNTERS_DB",
				Type:    client.Poll,
				Queries: []client.Path{{"COUNTERS", "Ethernet*", "Queues"}},
				TLS:     &tls.Config{InsecureSkipVerify: true},
			},
			wantNoti: []client.Notification{
				client.Connected{},
				client.Update{Path: []string{"COUNTERS", "Ethernet*", "Queues"},
					TS: time.Unix(0, 200), Val: emptyRespVal},
				client.Sync{},
				client.Update{Path: []string{"COUNTERS", "Ethernet*", "Queues"},
					TS: time.Unix(0, 200), Val: emptyRespVal},
				client.Sync{},
			},
		},
		{
			desc: "[poll][VirtualPath]poll query for COUNTERS/Ethernet4/Queues with field value change",
			poll: 3,
			q: client.Query{
				Target:  "COUNTERS_DB",
				Type:    client.Poll,
				Queries: []client.Path{{"COUNTERS", "Ethernet4", "Queues"}},
				TLS:     &tls.Config{InsecureSkipVerify: true},
			},
			wantNoti: []client.Notification{
				client.Connected{},
				client.Update{Path: []string{"COUNTERS", "Ethernet4", "Queues"},
					TS: time.Unix(0, 200), Val: queueOfEthernet4},
				client.Sync{},
				client.Update{Path: []string{"COUNTERS", "Ethernet4", "Queues"},
					TS: time.Unix(0, 200), Val: queueOfEthernet4},
				client.Sync{},
				client.Update{Path: []string{"COUNTERS", "Ethernet4", "Queues"},
					TS: time.Unix(0, 200), Val: queueOfEthernet4},
				client.Sync{},
				client.Update{Path: []string{"COUNTERS", "Ethernet4", "Queues"},
					TS: time.Unix(0, 200), Val: queueOfEthernet4},
				client.Sync{},
			},
		},
		{
			desc: "[poll][Non-DB]poll query for platform/cpu",
			poll: 3,
			q: client.Query{
				Target:  "OTHERS",
				Type:    client.Poll,
				Queries: []client.Path{{"platform", "cpu"}},
				TLS:     &tls.Config{InsecureSkipVerify: true},
			},
			wantNoti: []client.Notification{
				client.Connected{},
				client.Update{Path: []string{"platform", "cpu"},
					TS: time.Unix(0, 200), Val: emptyRespVal},
				client.Sync{},
				client.Update{Path: []string{"platform", "cpu"},
					TS: time.Unix(0, 200), Val: emptyRespVal},
				client.Sync{},
				client.Update{Path: []string{"platform", "cpu"},
					TS: time.Unix(0, 200), Val: emptyRespVal},
				client.Sync{},
				client.Update{Path: []string{"platform", "cpu"},
					TS: time.Unix(0, 200), Val: emptyRespVal},
				client.Sync{},
			},
		},
	}
	for _, tt := range tests {
		// Extra db preparation for this test case

		for _, prepare := range tt.prepares {
			switch prepare.op {
			case "hdel":
				hdelCountersDB(t, prepare.tableName+prepare.delimitor+prepare.tableKey, prepare.field)
			default:
				hsetCountersDB(t, prepare.tableName+prepare.delimitor+prepare.tableKey, prepare.field, prepare.value)
			}
		}
		time.Sleep(time.Millisecond * 1000)

		t.Run(tt.desc, func(t *testing.T) {
			q := tt.q
			q.Addrs = []string{dialinAddr}
			c := client.New()
			defer c.Close()
			var gotNoti []client.Notification
			q.NotificationHandler = func(n client.Notification) error {
				// t.Logf("reflect.TypeOf(n) %v :  %v", reflect.TypeOf(n), n)
				if nn, ok := n.(client.Update); ok {
					nn.TS = time.Unix(0, 200)
					gotNoti = append(gotNoti, nn)
				} else {
					gotNoti = append(gotNoti, n)
				}

				return nil
			}
			go func() {
				c.Subscribe(context.Background(), q)
				/*
					err := c.Subscribe(context.Background(), q)
					t.Log("c.Subscribe err:", err)
					switch {
					case tt.wantErr && err != nil:
						return
					case tt.wantErr && err == nil:
						t.Fatalf("c.Subscribe(): got nil error, expected non-nil")
					case !tt.wantErr && err != nil:
						t.Fatalf("c.Subscribe(): got error %v, expected nil", err)
					}
				*/
			}()
			// wait for half second for subscribeRequest to sync
			time.Sleep(time.Millisecond * 1000)
			for _, update := range tt.updates {
				switch update.op {
				case "hdel":
					hdelCountersDB(t, update.tableName+update.delimitor+update.tableKey, update.field)
				default:
					hsetCountersDB(t, update.tableName+update.delimitor+update.tableKey, update.field, update.value)
				}
				time.Sleep(time.Millisecond * 500)
			}
			// wait for one and half second for change to sync
			time.Sleep(time.Millisecond * 1500)

			for i := 0; i < tt.poll; i++ {
				err := c.Poll()
				switch {
				case err == nil && tt.wantPollErr != "":
					t.Errorf("c.Poll(): got nil error, expected non-nil %v", tt.wantPollErr)
				case err != nil && tt.wantPollErr == "":
					t.Errorf("c.Poll(): got error %v, expected nil", err)
				case err != nil && err.Error() != tt.wantPollErr:
					t.Errorf("c.Poll(): got error %v, expected error %v", err, tt.wantPollErr)
				}
			}

			//the manual update of SAI_PORT_STAT_PFC_7_RX_PKTS will be set back by SONiC, therefore it may get more Notification unexpectedly
			if tt.trimResult {
				gotNoti = gotNoti[:len(tt.wantNoti)]
			}
			// t.Log("\n Want: \n", tt.wantNoti)
			// t.Log("\n Got : \n", gotNoti)

			if tt.compareData {
				if diff := pretty.Compare(tt.wantNoti, gotNoti); diff != "" {
					t.Log("\n Want: \n", tt.wantNoti)
					t.Log("\n Got : \n", gotNoti)
					t.Errorf("unexpected updates:\n%s", diff)
				}
			} else {
				if !compareResponsePath(t, tt.wantNoti, gotNoti) {
					t.Errorf("Compare client.Notification failed")
					// t.Log("\n Want: \n", tt.wantNoti)
					// t.Log("\n Got : \n", gotNoti)
				}
			}
		})

		for _, recovery := range tt.recoveries {
			switch recovery.op {
			case "hdel":
				hdelCountersDB(t, recovery.tableName+recovery.delimitor+recovery.tableKey, recovery.field)
			default:
				hsetCountersDB(t, recovery.tableName+recovery.delimitor+recovery.tableKey, recovery.field, recovery.value)
			}
		}
		time.Sleep(time.Millisecond * 1000)
	}
}

func hsetCountersDB(t *testing.T, key, field, value string) {
	session := getSSHSession(t)
	defer session.Close()

	cmd := fmt.Sprintf("redis-cli -n 2 --csv hset %s %s %s", key, field, value)
	sshRedisCli(t, session, cmd)
}

func hdelCountersDB(t *testing.T, key, field string) {
	session := getSSHSession(t)
	defer session.Close()

	cmd := fmt.Sprintf("redis-cli -n 2 --csv hdel %s %s", key, field)
	// t.Log(cmd)
	sshRedisCli(t, session, cmd)
}

func TestGnmiSubscribe(t *testing.T) {
	runTestSubscribe(t)
}

func compareResponsePath(t *testing.T, wantNoti, gotNoti []client.Notification) bool {
	if len(wantNoti) != len(gotNoti) {
		t.Errorf("the length of wantNoti is different from that of gotNoti")
		return false
	}

	// var result bool = true
	for i := 0; i < len(wantNoti); i++ {
		// item := wantNoti[i]
		switch wantNoti[i].(type) {
		case client.Update:
			wantUpdate := wantNoti[i].(client.Update)
			gotUpdate, ok := gotNoti[i].(client.Update)
			if ok && wantUpdate.Path.Equal(gotUpdate.Path) {
				// fmt.Println("update", wantUpdate.Path, gotUpdate.Path)
				// fmt.Println("update", wantUpdate, gotUpdate)
				continue
			}
			t.Logf("\n Want: %v \n", wantUpdate.Path)
			t.Logf("\n Got: %v \n", gotUpdate.Path)
			t.Errorf("the path of wantNoti is different from that of gotNoti")
			return false
		}

		if wantNoti[i] != gotNoti[i] {
			t.Errorf("the type of wantNoti is different from that of gotNoti: %v, %v", wantNoti[i], gotNoti[i])
			return false
		}
	}
	return true
}

type op_t int

const (
	Delete  op_t = 1
	Replace op_t = 2
)

func runTestSet(t *testing.T, ctx context.Context, gClient pb.GNMIClient, pathTarget string,
	textPbPath string, wantRetCode codes.Code, wantRespVal interface{}, attributeData string, op op_t) {
	// Send request
	var pbPath pb.Path
	if err := proto.UnmarshalText(textPbPath, &pbPath); err != nil {
		t.Fatalf("error in unmarshaling path: %v %v", textPbPath, err)
	}
	req := &pb.SetRequest{}
	switch op {
	case Replace:
		//prefix := pb.Path{Target: pathTarget}
		var v *pb.TypedValue
		v = &pb.TypedValue{
			Value: &pb.TypedValue_JsonIetfVal{JsonIetfVal: extractJSON(attributeData)}}

		req = &pb.SetRequest{
			Replace: []*pb.Update{&pb.Update{Path: &pbPath, Val: v}},
		}
	case Delete:
		req = &pb.SetRequest{
			Delete: []*pb.Path{&pbPath},
		}
	}

	fmt.Println(req)
	_, err := gClient.Set(ctx, req)
	gotRetStatus, ok := status.FromError(err)
	if !ok {
		t.Fatal("got a non-grpc error from grpc call")
	}
	if gotRetStatus.Code() != wantRetCode {
		t.Log("err: ", err)
		t.Fatalf("got return code %v, want %v", gotRetStatus.Code(), wantRetCode)
	} else {
	}
}

func extractJSON(val string) []byte {
	jsonBytes, err := ioutil.ReadFile(val)
	if err == nil {
		return jsonBytes
	}
	return []byte(val)
}

// func TestGnmiSet(t *testing.T) {

// 	//t.Log("Start gNMI client")
// 	tlsConfig := &tls.Config{InsecureSkipVerify: true}
// 	opts := []grpc.DialOption{grpc.WithTransportCredentials(credentials.NewTLS(tlsConfig))}

// 	targetAddr := dialinAddr
// 	conn, err := grpc.Dial(targetAddr, opts...)
// 	if err != nil {
// 		t.Fatalf("Dialing to %q failed: %v", targetAddr, err)
// 	}
// 	defer conn.Close()

// 	gClient := pb.NewGNMIClient(conn)
// 	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
// 	defer cancel()

// 	var emptyRespVal interface{}

// 	tds := []struct {
// 		desc          string
// 		pathTarget    string
// 		textPbPath    string
// 		wantRetCode   codes.Code
// 		wantRespVal   interface{}
// 		attributeData string
// 		operation     op_t
// 		valTest       bool
// 	}{
// 		{
// 			desc:       "Set OC Interface MTU",
// 			pathTarget: "OC_YANG",
// 			textPbPath: `
//                         elem: <name: "openconfig-interfaces:interfaces" > elem:<name:"interface" key:<key:"name" value:"Ethernet4" > >
//                 `,
// 			attributeData: "../testdata/set_interface_mtu.json",
// 			wantRetCode:   codes.OK,
// 			wantRespVal:   emptyRespVal,
// 			operation:     Replace,
// 			valTest:       false,
// 		},
// 		// {
// 		// 	desc:       "Set OC Interface IP",
// 		// 	pathTarget: "OC_YANG",
// 		// 	textPbPath: `
// 		//             elem:<name:"openconfig-interfaces:interfaces" > elem:<name:"interface" key:<key:"name" value:"Ethernet4" > > elem:<name:"subinterfaces" > elem:<name:"subinterface" key:<key:"index" value:"0" > >
// 		//         `,
// 		// 	attributeData: "../testdata/set_interface_ipv4.json",
// 		// 	wantRetCode:   codes.OK,
// 		// 	wantRespVal:   emptyRespVal,
// 		// 	operation:     Replace,
// 		// 	valTest:       false,
// 		// },
// 		// {
// 		// 	desc:       "Delete OC Interface IP",
// 		// 	pathTarget: "OC_YANG",
// 		// 	textPbPath: `
// 		//             elem:<name:"openconfig-interfaces:interfaces" > elem:<name:"interface" key:<key:"name" value:"Ethernet4" > > elem:<name:"subinterfaces" > elem:<name:"subinterface" key:<key:"index" value:"0" > > elem:<name: "ipv4" > elem:<name: "addresses" > elem:<name:"address" key:<key:"ip" value:"9.9.9.9" > >
// 		//         `,
// 		// 	attributeData: "",
// 		// 	wantRetCode:   codes.OK,
// 		// 	wantRespVal:   emptyRespVal,
// 		// 	operation:     Delete,
// 		// 	valTest:       false,
// 		// },
// 	}

// 	for _, td := range tds {
// 		if td.valTest == true {
// 			// wait for 2 seconds for change to sync
// 			time.Sleep(2 * time.Second)
// 			t.Run(td.desc, func(t *testing.T) {
// 				runTestGet(t, ctx, gClient, td.pathTarget, td.textPbPath, td.wantRetCode, td.wantRespVal, td.valTest)
// 			})
// 		} else {
// 			t.Run(td.desc, func(t *testing.T) {
// 				runTestSet(t, ctx, gClient, td.pathTarget, td.textPbPath, td.wantRetCode, td.wantRespVal, td.attributeData, td.operation)
// 			})
// 		}
// 	}
// }

func getEthernetWildcardPfcByte(t *testing.T, field string) []uint8 {
	var result map[string]map[string]string = make(map[string]map[string]string)
	prepareData(t, "aliasNameMap")
	prepareData(t, "portNameMap")
	// fmt.Println("aliasNameMap", len(aliasNameMap), aliasNameMap)
	// fmt.Println("portNameMap", len(portNameMap), portNameMap)
	for alias, portName := range aliasNameMap {
		redisKey := "COUNTERS:" + portNameMap[portName]
		// cmd := fmt.Sprintf("redis-cli -n 2 --csv hgetall %s", redisKey)
		cmd := fmt.Sprintf("redis-cli -n 2 --csv hget %s %s", redisKey, field)
		sshSession := getSSHSession(t)

		// fmt.Printf("[Preparing compared data] Getting data of %v now\n", alias)
		tmpResult := sshHget(t, sshSession, cmd)

		result[alias] = map[string]string{field: tmpResult}

		sshSession.Close()
	}
	jsonBytes, _ := json.Marshal(result)
	// fmt.Println(result)
	// fmt.Println(string(jsonBytes))
	return jsonBytes
}

func TestGNMIDialOutPublish(t *testing.T) {
	// clear
	exe_cmd(t, "redis-cli -n 4 hdel \"TELEMETRY_CLIENT|Subscription_HS_RDMA\" path_target dst_group report_type paths")
	exe_cmd(t, "redis-cli -n 4 hdel \"TELEMETRY_CLIENT|DestinationGroup_HS\" dst_addr")

	//global set
	exe_cmd(t, "redis-cli -n 4 hset \"TELEMETRY_CLIENT|Global\" retry_interval 1")
	exe_cmd(t, "redis-cli -n 4 hset \"TELEMETRY_CLIENT|Global\" encoding JSON_IETF")
	exe_cmd(t, "redis-cli -n 4 hset \"TELEMETRY_CLIENT|Global\" unidirectional true")
	// exe_cmd(t, "redis-cli -n 4 hset \"TELEMETRY_CLIENT|Global\" src_ip  192.168.3.20")

	prepareData(t, "portNameMap")
	prepareData(t, "aliasNameMap")

	portNameMapUpdate := copyMap(portNameMap)
	portNameMapUpdate["test_field"] = "test_value"
	portNameMapUpdateByte, _ := json.Marshal(portNameMapUpdate)

	oid4, ok := portNameMap["Ethernet4"]
	if !ok {
		t.Fatalf("[TestSubscribe] cannot find the oid of Ethernet4")
	} else {
		t.Logf("Ethernet4=%v", oid4)
	}

	countersPortNameMapByte, _ := json.Marshal(portNameMap)
	prepareData(t, "countersEthernetWildcardByte")
	prepareData(t, "countersEthernetWildcardPfc7Byte")

	prepareData(t, "queueNameMap")
	prepareData(t, "queueOfEthernet4")
	if queueOfEthernet4Byte == nil {
		queueOfEthernet4Byte, _ = json.Marshal(queueOfEthernet4)
	}

	tests := []struct {
		desc     string
		prepares []tablePathValue // extra preparation of redis db
		cmds     []string         // commands to execute
		sop      ServerOp         // Server operation done after commonds
		updates  []tablePathValue // Update to db data
		waitTime time.Duration    // Wait ftime after server operation

		wantErr     bool
		collector   string
		wantRespVal interface{}
		trimResult  bool
		compareData bool
	}{
		{
			// pattern:
			// <SubscribeResponse_Update>
			// <SubscribeResponse_Update>
			// ...
			desc: "[Periodic][RealPath]DialOut to first collector in periodic mode(COUNTERS_PORT_NAME_MAP)",
			cmds: []string{
				"redis-cli -n 4 hset \"TELEMETRY_CLIENT|DestinationGroup_HS\" dst_addr " + dialoutAddr1 + "," + dialoutAddr2,
				"redis-cli -n 4 hmset \"TELEMETRY_CLIENT|Subscription_HS_RDMA\" path_target COUNTERS_DB dst_group HS report_type periodic report_interval 1000 paths COUNTERS_PORT_NAME_MAP",
			},
			collector: "s1",
			sop:       S2Stop,
			waitTime:  time.Second * 3,
			wantRespVal: []*pb.SubscribeResponse{
				&pb.SubscribeResponse{
					Response: &pb.SubscribeResponse_Update{
						Update: &pb.Notification{
							//Timestamp: GetTimestamp(),
							//Prefix:    prefix,
							Update: []*pb.Update{
								{
									Val: &pb.TypedValue{
										Value: &pb.TypedValue_JsonIetfVal{
											JsonIetfVal: countersPortNameMapByte,
										}},
									Path: &pb.Path{
										Elem: []*pb.PathElem{
											&pb.PathElem{
												Name: "COUNTERS_PORT_NAME_MAP",
											},
										},
									},
								},
							},
						},
					},
				},
				&pb.SubscribeResponse{
					Response: &pb.SubscribeResponse_Update{
						Update: &pb.Notification{
							//Timestamp: GetTimestamp(),
							//Prefix:    prefix,
							Update: []*pb.Update{
								{Val: &pb.TypedValue{
									Value: &pb.TypedValue_JsonIetfVal{
										JsonIetfVal: countersPortNameMapByte,
									}},
								//Path: GetPath(),
								},
							},
						},
					},
				},
			},
			compareData: true,
		},
		{
			// pattern:
			// <SubscribeResponse_Update>
			// <SubscribeResponse_Update>
			// ...
			desc: "[Periodic][VirtualPath*]DialOut to first collector in periodic mode(COUNTERS/Ethernet*)",
			cmds: []string{
				"redis-cli -n 4 hset \"TELEMETRY_CLIENT|DestinationGroup_HS\" dst_addr " + dialoutAddr1 + "," + dialoutAddr2,
				"redis-cli -n 4 hmset \"TELEMETRY_CLIENT|Subscription_HS_RDMA\" path_target COUNTERS_DB dst_group HS report_type periodic report_interval 1000 paths COUNTERS/Ethernet*",
			},
			collector: "s1",
			sop:       S2Stop,
			waitTime:  time.Second * 3,
			wantRespVal: []*pb.SubscribeResponse{
				&pb.SubscribeResponse{
					Response: &pb.SubscribeResponse_Update{
						Update: &pb.Notification{
							//Timestamp: GetTimestamp(),
							//Prefix:    prefix,
							Update: []*pb.Update{
								{
									Val: &pb.TypedValue{
										Value: &pb.TypedValue_JsonIetfVal{
											JsonIetfVal: ethernetWildcardByte,
										}},
									Path: &pb.Path{
										Elem: []*pb.PathElem{
											&pb.PathElem{
												Name: "COUNTERS",
											},
											&pb.PathElem{
												Name: "Ethernet*",
											},
										},
									},
									//Path: GetPath(),
								},
							},
						},
					},
				},
				&pb.SubscribeResponse{
					Response: &pb.SubscribeResponse_Update{
						Update: &pb.Notification{
							//Timestamp: GetTimestamp(),
							//Prefix:    prefix,
							Update: []*pb.Update{
								{Val: &pb.TypedValue{
									Value: &pb.TypedValue_JsonIetfVal{
										JsonIetfVal: ethernetWildcardByte,
									}},
									Path: &pb.Path{
										Elem: []*pb.PathElem{
											&pb.PathElem{
												Name: "COUNTERS",
											},
											&pb.PathElem{
												Name: "Ethernet*",
											},
										},
									},
									//Path: GetPath(),
								},
							},
						},
					},
				},
			},
		},
		{
			// pattern:
			// <SubscribeResponse_Update>
			// <SubscribeResponse_Update>
			// ...
			desc: "[Periodic][VirutalPath]DialOut to first collector in periodic mode upon failure of first collector(COUNTERS/Ethernet4/Queues)",
			cmds: []string{
				"redis-cli -n 4 hset \"TELEMETRY_CLIENT|DestinationGroup_HS\" dst_addr " + dialoutAddr1 + "," + dialoutAddr2,
				"redis-cli -n 4 hmset \"TELEMETRY_CLIENT|Subscription_HS_RDMA\" path_target COUNTERS_DB dst_group HS report_type periodic report_interval 1000 paths COUNTERS/Ethernet4/Queues",
			},
			collector: "s2",
			sop:       S1Stop,
			waitTime:  time.Second * 4,
			wantRespVal: []*pb.SubscribeResponse{
				&pb.SubscribeResponse{
					Response: &pb.SubscribeResponse_Update{
						Update: &pb.Notification{
							//Timestamp: GetTimestamp(),
							//Prefix:    prefix,
							Update: []*pb.Update{
								{Val: &pb.TypedValue{
									Value: &pb.TypedValue_JsonIetfVal{
										JsonIetfVal: queueOfEthernet4Byte,
									}},
									Path: &pb.Path{
										Elem: []*pb.PathElem{
											&pb.PathElem{
												Name: "COUNTERS",
											},
											&pb.PathElem{
												Name: "Ethernet4",
											},
											&pb.PathElem{
												Name: "Queues",
											},
										},
									},
									//Path: GetPath(),
								},
							},
						},
					},
				},
				&pb.SubscribeResponse{
					Response: &pb.SubscribeResponse_Update{
						Update: &pb.Notification{
							//Timestamp: GetTimestamp(),
							//Prefix:    prefix,
							Update: []*pb.Update{
								{Val: &pb.TypedValue{
									Value: &pb.TypedValue_JsonIetfVal{
										JsonIetfVal: queueOfEthernet4Byte,
									}},
									Path: &pb.Path{
										Elem: []*pb.PathElem{
											&pb.PathElem{
												Name: "COUNTERS",
											},
											&pb.PathElem{
												Name: "Ethernet4",
											},
											&pb.PathElem{
												Name: "Queues",
											},
										},
									},
									//Path: GetPath(),
								},
							},
						},
					},
				},
			},
		},
		{
			// pattern:
			// <SubscribeResponse_Update>
			// <SubscribeResponse_Update>
			// ...
			desc: "[Periodic][VirutalPath]DialOut to first collector in periodic mode (platform/cpu)",
			cmds: []string{
				"redis-cli -n 4 hset \"TELEMETRY_CLIENT|DestinationGroup_HS\" dst_addr " + dialoutAddr1 + "," + dialoutAddr2,
				"redis-cli -n 4 hmset \"TELEMETRY_CLIENT|Subscription_HS_RDMA\" path_target OTHERS dst_group HS report_type periodic report_interval 1000 paths platform/cpu",
			},
			collector: "s1",
			sop:       S2Stop,
			waitTime:  time.Second * 3,
			wantRespVal: []*pb.SubscribeResponse{
				&pb.SubscribeResponse{
					Response: &pb.SubscribeResponse_Update{
						Update: &pb.Notification{
							//Timestamp: GetTimestamp(),
							//Prefix:    prefix,
							Update: []*pb.Update{
								{Val: &pb.TypedValue{
									Value: &pb.TypedValue_JsonIetfVal{
										JsonIetfVal: make([]uint8, 0),
									}},
									Path: &pb.Path{
										Elem: []*pb.PathElem{
											&pb.PathElem{
												Name: "platform",
											},
											&pb.PathElem{
												Name: "cpu",
											},
										},
									},
									//Path: GetPath(),
								},
							},
						},
					},
				},
				&pb.SubscribeResponse{
					Response: &pb.SubscribeResponse_Update{
						Update: &pb.Notification{
							//Timestamp: GetTimestamp(),
							//Prefix:    prefix,
							Update: []*pb.Update{
								{Val: &pb.TypedValue{
									Value: &pb.TypedValue_JsonIetfVal{
										JsonIetfVal: make([]uint8, 0),
									}},
									Path: &pb.Path{
										Elem: []*pb.PathElem{
											&pb.PathElem{
												Name: "platform",
											},
											&pb.PathElem{
												Name: "cpu",
											},
										},
									},
									//Path: GetPath(),
								},
							},
						},
					},
				},
			},
		},
		{
			// pattern:
			// <SubscribeResponse_Update>
			// <SubscribeResponse_SyncResponse>
			// <SubscribeResponse_Update>
			// <SubscribeResponse_Update>
			// ...
			desc: "[Stream][RealPath]DialOut to second collector in stream mode(COUNTERS_PORT_NAME_MAP)",
			cmds: []string{
				"redis-cli -n 4 hset \"TELEMETRY_CLIENT|DestinationGroup_HS\" dst_addr " + dialoutAddr1 + "," + dialoutAddr2,
				"redis-cli -n 4 hmset \"TELEMETRY_CLIENT|Subscription_HS_RDMA\" path_target COUNTERS_DB dst_group HS report_type stream paths COUNTERS_PORT_NAME_MAP",
				"redis-cli -n 4 hmdel \"TELEMETRY_CLIENT|Subscription_HS_RDMA\" report_interval",
			},
			collector: "s1",
			sop:       S2Stop,
			updates: []tablePathValue{
				{
					dbName:    "COUNTERS_DB",
					tableName: "COUNTERS_PORT_NAME_MAP",
					field:     "test_field",
					value:     "test_value",
				},
				{
					dbName:    "COUNTERS_DB",
					tableName: "COUNTERS_PORT_NAME_MAP",
					field:     "test_field",
					op:        "hdel",
				},
			},
			waitTime: time.Second,
			wantRespVal: []*pb.SubscribeResponse{
				&pb.SubscribeResponse{
					Response: &pb.SubscribeResponse_Update{
						Update: &pb.Notification{
							Update: []*pb.Update{
								{Val: &pb.TypedValue{
									Value: &pb.TypedValue_JsonIetfVal{
										JsonIetfVal: countersPortNameMapByte,
									}},
									Path: &pb.Path{
										Elem: []*pb.PathElem{
											&pb.PathElem{
												Name: "COUNTERS_PORT_NAME_MAP",
											},
										},
									},
								},
							},
						},
					},
				},
				&pb.SubscribeResponse{
					Response: &pb.SubscribeResponse_SyncResponse{
						SyncResponse: true,
					},
				},
				&pb.SubscribeResponse{
					Response: &pb.SubscribeResponse_Update{
						Update: &pb.Notification{
							Update: []*pb.Update{
								{Val: &pb.TypedValue{
									Value: &pb.TypedValue_JsonIetfVal{
										JsonIetfVal: portNameMapUpdateByte,
									}},
								},
							},
						},
					},
				},
			},
			trimResult:  true,
			compareData: true,
		},
		{
			// pattern:
			// <SubscribeResponse_Update>
			// <SubscribeResponse_SyncResponse>
			// ...
			desc: "[Stream][VirtualPath*]DialOut to second collector in stream mode upon failure of first collector(COUNTERS/Ethernet*/SAI_PORT_STAT_PFC_7_RX_PKTS)",
			cmds: []string{
				"redis-cli -n 4 hset \"TELEMETRY_CLIENT|DestinationGroup_HS\" dst_addr " + dialoutAddr1 + "," + dialoutAddr2,
				"redis-cli -n 4 hmset \"TELEMETRY_CLIENT|Subscription_HS_RDMA\" path_target COUNTERS_DB dst_group HS report_type stream paths COUNTERS/Ethernet*/SAI_PORT_STAT_PFC_7_RX_PKTS",
				"redis-cli -n 4 hmdel \"TELEMETRY_CLIENT|Subscription_HS_RDMA\" report_interval",
			},
			collector: "s2",
			sop:       S1Stop,
			updates: []tablePathValue{
				{
					dbName:    "COUNTERS_DB",
					tableName: "COUNTERS",
					tableKey:  oid4,
					delimitor: ":",
					field:     "SAI_PORT_STAT_PFC_7_RX_PKTS",
					value:     "3", // be changed to 3 from 2
				},
				{
					dbName:    "COUNTERS_DB",
					tableName: "COUNTERS",
					tableKey:  oid4,
					delimitor: ":",
					field:     "SAI_PORT_STAT_PFC_7_RX_PKTS",
					value:     "3", // be changed to 3 from 2
				},
				{
					dbName:    "COUNTERS_DB",
					tableName: "COUNTERS",
					tableKey:  oid4,
					delimitor: ":",
					field:     "SAI_PORT_STAT_PFC_7_RX_PKTS",
					value:     "3", // be changed to 3 from 2
				},
				// {
				// 	dbName:    "COUNTERS_DB",
				// 	tableName: "COUNTERS",
				// 	tableKey:  oid4,
				// 	delimitor: ":",
				// 	field:     "SAI_PORT_STAT_PFC_7_RX_PKTS",
				// 	value:     "2", // be changed to 2 from 3
				// },
			},
			waitTime: 2*time.Second + time.Second,
			wantRespVal: []*pb.SubscribeResponse{
				&pb.SubscribeResponse{
					Response: &pb.SubscribeResponse_Update{
						Update: &pb.Notification{
							Update: []*pb.Update{
								{Val: &pb.TypedValue{
									Value: &pb.TypedValue_JsonIetfVal{
										JsonIetfVal: ethernetWildcardPfc7Byte,
									}},
									Path: &pb.Path{
										Elem: []*pb.PathElem{
											&pb.PathElem{
												Name: "COUNTERS",
											},
											&pb.PathElem{
												Name: "Ethernet*",
											},
											&pb.PathElem{
												Name: "SAI_PORT_STAT_PFC_7_RX_PKTS",
											},
										},
									},
								},
							},
						},
					},
				},
				&pb.SubscribeResponse{
					Response: &pb.SubscribeResponse_SyncResponse{
						SyncResponse: true,
					},
				},
			},
			trimResult: true,
		},
	}

	for _, tt := range tests {
		serverOp(t, S1Start)
		serverOp(t, S2Start)
		t.Run(tt.desc, func(t *testing.T) {
			var store []*pb.SubscribeResponse
			if tt.collector == "s1" {
				s1.SetDataStore(&store)
			} else {
				s2.SetDataStore(&store)
			}
			// Extra cmd preparation for this test case
			for _, cmd := range tt.cmds {
				// fmt.Println(cmd)
				exe_cmd(t, cmd)
			}
			serverOp(t, tt.sop)
			time.Sleep(time.Millisecond * 1000)

			for _, update := range tt.updates {
				switch update.op {
				case "hdel":
					hdelCountersDB(t, update.tableName+update.delimitor+update.tableKey, update.field)
				default:
					hsetCountersDB(t, update.tableName+update.delimitor+update.tableKey, update.field, update.value)
				}
				time.Sleep(time.Millisecond * 500)
			}
			if tt.waitTime != 0 {
				time.Sleep(tt.waitTime)
			}
			wantRespVal := tt.wantRespVal.([]*pb.SubscribeResponse)
			if len(store) < len(wantRespVal) {
				t.Logf("len not match %v %s %v", len(store), " : ", len(wantRespVal))
				t.Logf("want: %v", wantRespVal)
				t.Fatal("got: ", store)
			}

			if tt.trimResult {
				store = store[:len(wantRespVal)]
			}
			slen := len(store)
			wlen := len(wantRespVal)

			// fmt.Println("@@@", tt.desc)
			// for _, tmp := range wantRespVal {
			// 	switch tmp.GetResponse().(type) {
			// 	case *pb.SubscribeResponse_SyncResponse:
			// 		fmt.Print("SyncResponse", "\t")
			// 	case *pb.SubscribeResponse_Update:
			// 		fmt.Print("Update", "\t")
			// 	}
			// }
			// fmt.Println()
			// for _, tmp := range store {
			// 	switch tmp.GetResponse().(type) {
			// 	case *pb.SubscribeResponse_SyncResponse:
			// 		fmt.Print("SyncResponse", "\t")
			// 	case *pb.SubscribeResponse_Update:
			// 		fmt.Print("Update", "\t")
			// 	}
			// }
			// fmt.Println()
			// for _, tmp := range store {
			// 	fmt.Println(tmp)
			// }

			// for idx, resp := range wantRespVal {
			// 	switch store[slen-wlen+idx].GetResponse().(type) {
			// 	case *pb.SubscribeResponse_SyncResponse:
			// 		if _, ok := resp.GetResponse().(*pb.SubscribeResponse_SyncResponse); !ok {
			// 			t.Fatalf("Expecting %v, got SyncResponse", resp.GetResponse())
			// 		}
			// 	case *pb.SubscribeResponse_Update:
			// 		if len(resp.GetUpdate().GetUpdate()) == 0 {
			// 			fmt.Println("0", "idx", idx, "content", resp)
			// 			fmt.Println(wantRespVal)
			// 		}
			// 		compareUpdateValue(t, resp.GetUpdate(), store[slen-wlen+idx].GetUpdate())
			// 	}
			// }
			for idx, resp := range wantRespVal {
				switch resp.GetResponse().(type) {
				case *pb.SubscribeResponse_SyncResponse:
					if _, ok := store[slen-wlen+idx].GetResponse().(*pb.SubscribeResponse_SyncResponse); !ok {
						t.Fatalf("Expecting SyncResponse, got %v", store[slen-wlen+idx].GetResponse())
					}
				case *pb.SubscribeResponse_Update:
					// if len(resp.GetUpdate().GetUpdate()) == 0 {
					// 	fmt.Println("0", "idx", idx, "content", resp)
					// 	fmt.Println(wantRespVal)
					// }
					if _, ok := store[slen-wlen+idx].GetResponse().(*pb.SubscribeResponse_Update); !ok {
						t.Fatalf("Expecting %v, got SyncResponse", resp.GetResponse())
					} else {
						if tt.compareData {
							compareUpdateValue(t, resp.GetUpdate(), store[slen-wlen+idx].GetUpdate())
						} else {
							compareUpdatePath(t, resp.GetUpdate(), store[slen-wlen+idx].GetUpdate())
						}
					}
				}
			}
		})
		//recover
		exe_cmd(t, "redis-cli -n 4 hdel \"TELEMETRY_CLIENT|Subscription_HS_RDMA\" path_target dst_group report_type paths")
		exe_cmd(t, "redis-cli -n 4 hdel \"TELEMETRY_CLIENT|DestinationGroup_HS\" dst_addr")

		serverOp(t, S1Stop)
		serverOp(t, S2Stop)
	}

	// recover
	exe_cmd(t, "redis-cli -n 4 hdel \"TELEMETRY_CLIENT|Subscription_HS_RDMA\" path_target dst_group report_type paths")
	exe_cmd(t, "redis-cli -n 4 hdel \"TELEMETRY_CLIENT|DestinationGroup_HS\" dst_addr")
	exe_cmd(t, "redis-cli -n 4 hset \"TELEMETRY_CLIENT|Global\" retry_interval 5")
}

func exe_cmd(t *testing.T, cmd string) {
	session := getSSHSession(t)
	defer session.Close()

	sshRedisCli(t, session, cmd)
}

type ServerOp int

const (
	_                = iota
	S1Start ServerOp = iota
	S1Stop
	S2Start
	S2Stop
)

var s1, s2 *sds.Server

func serverOp(t *testing.T, sop ServerOp) {
	cfg := &sds.Config{Port: 8080}
	var tmpStore []*pb.SubscribeResponse
	switch sop {
	case S1Stop:
		s1.Stop()
	case S2Stop:
		s2.Stop()
	case S1Start:
		s1 = createServer(t, cfg)
		s1.SetDataStore(&tmpStore)
		go runServer(t, s1)
	case S2Start:
		cfg.Port = 8081
		s2 = createServer(t, cfg)
		s2.SetDataStore(&tmpStore)
		go runServer(t, s2)
	}
}

func createServer(t *testing.T, cfg *sds.Config) *sds.Server {
	certificate, err := testcert.NewCert()
	if err != nil {
		t.Fatalf("could not load server key pair: %s", err)
	}
	tlsCfg := &tls.Config{
		ClientAuth:   tls.RequestClientCert,
		Certificates: []tls.Certificate{certificate},
	}

	opts := []grpc.ServerOption{grpc.Creds(credentials.NewTLS(tlsCfg))}

	s, err := sds.NewServer(cfg, opts)
	if err != nil {
		t.Fatalf("Failed to create gNMIDialOut server: %v", err)
	}
	return s
}

func runServer(t *testing.T, s *sds.Server) {
	//t.Log("Starting RPC server on address:", s.Address())
	err := s.Serve() // blocks until close
	if err != nil {
		t.Fatalf("gRPC server err: %v", err)
	}
	//t.Log("Exiting RPC server on address", s.Address())
}

func compareUpdatePath(t *testing.T, want *pb.Notification, got *pb.Notification) {
	gotUpdates := got.GetUpdate()
	if len(gotUpdates) != 1 {
		t.Fatalf("got %d updates in the notification, want 1", len(gotUpdates))
	}

	wantUpdates := want.GetUpdate()
	if len(wantUpdates) != 1 {
		t.Fatalf("got %d updates in the notification, want 1", len(wantUpdates))
	}

	gotPath := gotUpdates[0].GetPath().GetElem()
	wantPath := wantUpdates[0].GetPath().GetElem()

	if len(gotPath) != len(wantPath) {
		t.Errorf("got path: %v,\nwant path %v", gotPath, wantPath)
		return
	}
	for i := range gotPath {
		if gotPath[i].GetName() != wantPath[i].GetName() {
			t.Errorf("got path: %v,\nwant path %v", gotPath, wantPath)
			return
		}
		if !reflect.DeepEqual(gotPath[i].GetKey(), wantPath[i].GetKey()) {
			t.Errorf("got path: %v,\nwant path %v", gotPath, wantPath)
			return
		}
	}

}

func compareUpdateValue(t *testing.T, want *pb.Notification, got *pb.Notification) {
	var wantRespVal interface{}
	var gotVal interface{}
	var err error

	updates := got.GetUpdate()
	if len(updates) != 1 {
		t.Fatalf("got %d updates in the notification, want 1", len(updates))
	}
	gotValTyped := updates[0].GetVal()

	updates = want.GetUpdate()
	// fmt.Println("\ngot", got, "\n", updates)
	if len(updates) == 0 {
		fmt.Println("0", want)
	}
	wantRespValTyped := updates[0].GetVal()

	//if !reflect.DeepEqual(val, wantRespVal) {
	//	t.Errorf("got: %v (%T),\nwant %v (%T)", val, val, wantRespVal, wantRespVal)
	//}

	if gotValTyped.GetJsonIetfVal() == nil {
		gotVal, err = value.ToScalar(gotValTyped)
		if err != nil {
			t.Errorf("got: %v, want a scalar value", gotVal)
		}
		wantRespVal, _ = value.ToScalar(wantRespValTyped)
	} else {
		// Unmarshal json data to gotVal container for comparison
		if err = json.Unmarshal(gotValTyped.GetJsonIetfVal(), &gotVal); err != nil {
			t.Fatalf("error in unmarshaling IETF JSON data to json container: %v", err)
		}
		if err = json.Unmarshal(wantRespValTyped.GetJsonIetfVal(), &wantRespVal); err != nil {
			t.Fatalf("error in unmarshaling IETF JSON data to json container: %v", err)
		}
	}

	if !reflect.DeepEqual(gotVal, wantRespVal) {
		t.Errorf("got: %v (%T),\nwant %v (%T)", gotVal, gotVal, wantRespVal, wantRespVal)
	}

}
