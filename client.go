package main

import (
	"flag"
	"fmt"
	"net"
	"os"
	"runtime"
	"strconv"
	"strings"
	"time"
)

//client端，提供服务，运行在家里有网站的电脑中

var host *string = flag.String("host", "127.0.0.1", "请输入服务器ip")
var remotePort *string = flag.String("remotePort", "20012", "服务器地址端口")
var localPort *string = flag.String("localPort", "8000", "本地端口")

//与browser相关的conn
type browser struct {
	conn net.Conn
	er   chan bool
	writ chan bool
	recv chan []byte
	send chan []byte
}

//读取browser过来的数据
func (b browser) read() {

	for {
		var recv []byte = make([]byte, 10240)
		n, err := b.conn.Read(recv)
		if err != nil {

			b.writ <- true
			b.er <- true
			//fmt.Println("读取browser失败", err)
			break
		}
		b.recv <- recv[:n]
	}
}

//把数据发送给browser
func (b browser) write() {

	for {
		var send []byte = make([]byte, 10240)
		select {
		case send = <-b.send:
			b.conn.Write(send)
		case <-b.writ:
			//fmt.Println("写入browser进程关闭")
			break
		}
	}

}

//与server相关的conn
type server struct {
	conn net.Conn
	er   chan bool
	writ chan bool
	recv chan []byte //client端接收数据通道
	send chan []byte
}

//读取server过来的数据
func (ser *server) read() {
	//isheart与timeout共同判断是不是自己设定的SetReadDeadline
	var isheart bool = false
	//20秒发一次心跳包
	ser.conn.SetReadDeadline(time.Now().Add(time.Second * 20))
	for {
		var recv []byte = make([]byte, 10240)
		n, err := ser.conn.Read(recv) //阻塞，读取server发过来的数据
		if err != nil {
			if strings.Contains(err.Error(), "timeout") && !isheart {
				ser.conn.Write([]byte("hh")) //发送心跳包
				//4秒时间收心跳包
				ser.conn.SetReadDeadline(time.Now().Add(time.Second * 4))
				isheart = true
				continue
			}
			//浏览器有可能连接上不发消息就断开，此时就发一个0，为了与服务器一直有一条tcp通路
			ser.recv <- []byte("0")
			ser.er <- true
			ser.writ <- true
			//fmt.Println("没收到心跳包或者server关闭，关闭此条tcp", err)
			break
		}
		//如果收到是心跳包
		if recv[0] == 'h' && recv[1] == 'h' {
			//fmt.Println("收到心跳包")
			ser.conn.SetReadDeadline(time.Now().Add(time.Second * 20)) //20秒后断开连接
			isheart = false
			continue
		}
		ser.recv <- recv[:n] //收到了server的通知，然后 handle()进行处理
	}
}

//把数据发送给server
func (ser server) write() {
	for {
		var send []byte = make([]byte, 10240)
		select {
		case send = <-ser.send: //收到响应数据后发送给 server 端
			ser.conn.Write(send)
		case <-ser.writ:
			//fmt.Println("写入server进程关闭")
			break
		}
	}
}

func main() {
	runtime.GOMAXPROCS(runtime.NumCPU())
	flag.Parse()
	if flag.NFlag() != 3 {
		flag.PrintDefaults()
		os.Exit(1)
	}
	local, _ := strconv.Atoi(*localPort)
	remote, _ := strconv.Atoi(*remotePort)
	if !(local >= 0 && local < 65536) {
		fmt.Println("端口设置错误")
		os.Exit(1)
	}
	if !(remote >= 0 && remote < 65536) {
		fmt.Println("端口设置错误")
		os.Exit(1)
	}
	target := net.JoinHostPort(*host, *remotePort)
	for {
		//链接端口
		serverconn := dail(target)
		recv := make(chan []byte)
		send := make(chan []byte)
		//1个位置是为了防止两个读取线程一个退出后另一个永远卡住
		er := make(chan bool, 1)
		writ := make(chan bool)
		next := make(chan bool)
		server := &server{serverconn, er, writ, recv, send}
		go server.read()  //for()开始读 server 端的数据
		go server.write() //for()开始给 server 写入数据
		go handle(server, next)
		<-next
	}

}

//显示错误
func log(err error) {
	if err != nil {
		fmt.Printf("出现错误： %v\n", err)
	}
}

//显示错误并退出
func logExit(err error) {
	if err != nil {
		fmt.Printf("出现错误，退出线程： %v\n", err)
		runtime.Goexit()
	}
}

//显示错误并关闭链接，退出线程
func logClose(err error, conn net.Conn) {
	if err != nil {
		//fmt.Println("对方已关闭", err)
		runtime.Goexit()
	}
}

//链接端口
func dail(hostport string) net.Conn {
	conn, err := net.Dial("tcp", hostport)
	logExit(err)
	return conn
}

//两个socket衔接相关处理
func handle(server *server, next chan bool) {
	var serverrecv = make([]byte, 10240)
	fmt.Println("等待server发来消息")
	serverrecv = <-server.recv //阻塞这里等待server传来数据再链接browser
	//连接上，下一个tcp连上服务器
	next <- true
	//fmt.Println("开始新的tcp链接，发来的消息是：", string(serverrecv))
	//server发来数据，链接本地8000端口
	serverconn := dail("127.0.0.1:" + *localPort)
	recv := make(chan []byte)
	send := make(chan []byte)
	er := make(chan bool, 1)
	writ := make(chan bool)
	browse := &browser{serverconn, er, writ, recv, send}
	go browse.read()          //for() 阻塞，一直读从 browse 来的数据
	go browse.write()         //for() 阻塞，一直给 browse 写数据
	browse.send <- serverrecv //browse 接收请求数据，然后写入 browse.write() 处理

	for {
		var serverrecv = make([]byte, 10240)
		var browserrecv = make([]byte, 10240)
		select {
		case serverrecv = <-server.recv:
			if serverrecv[0] != '0' {
				browse.send <- serverrecv
			}

		case browserrecv = <-browse.recv: //browse 返回响应的数据
			server.send <- browserrecv    //给 server 响应数据
		case <-server.er:
			//fmt.Println("server关闭了，关闭server与browse")
			server.conn.Close()
			browse.conn.Close()
			runtime.Goexit()
		case <-browse.er:
			//fmt.Println("browse关闭了，关闭server与browse")
			server.conn.Close()
			browse.conn.Close()
			runtime.Goexit()
		}
	}
}
