package main

import (
	"bytes"
	"crypto/tls"
	"crypto/x509"
	"encoding/binary"
	"encoding/gob"
	"errors"
	"github.com/Unknwon/goconfig"
	"io"
	"io/ioutil"
	"log"
	"net"
	"strconv"
	"time"
)

const (
	version = "0.4.0"

	verSocks5 = 0x05

	cmdConnect = 0x01

	atypIPv4Address = 0x01
	atypDomainName  = 0x03
	atypIPv6Address = 0x04

	reqtypeTCP  = 0x01
	reqtypeBIND = 0x02
	reqtypeUDP  = 0x03

	repSucceeded = 0x00

	rsvReserved = 0x00

	login      = 0
	connection = 1
)

func main() {
	//log.SetFlags(log.Lshortfile) //debug时开启

	//读取证书文件
	rootPEM, err := ioutil.ReadFile("cert.pem")
	if err != nil {
		log.Println("读取 cert.pem 出错：", err, "请检查文件是否存在")
		return
	}
	roots := x509.NewCertPool()
	ok := roots.AppendCertsFromPEM(rootPEM)
	if !ok {
		log.Println("证书分析失败，请证书文件是否正确")
		return
	}

	//加载配置文件
	cfg, err := goconfig.LoadConfigFile("client.ini")
	if err != nil {
		log.Println("配置文件加载失败，自动重置配置文件：", err)
		cfg, err = goconfig.LoadFromData([]byte{})
		if err != nil {
			log.Println(err)
			return
		}
	}

	var (
		port, ok1       = cfg.MustValueSet("client", "port", "7071")
		key, ok2        = cfg.MustValueSet("client", "key", "eGauUecvzS05U5DIsxAN4n2hadmRTZGBqNd2zsCkrvwEBbqoITj36mAMk4Unw6Pr")
		serverHost, ok3 = cfg.MustValueSet("server", "host", "127.0.0.1")
		serverPort, ok4 = cfg.MustValueSet("server", "port", "8081")
	)

	//如果缺少配置则保存为默认配置
	if ok1 || ok2 || ok3 || ok4 {
		err = goconfig.SaveConfigFile(cfg, "client.ini")
		if err != nil {
			log.Println("配置文件保存失败：", err)
		}
	}

	ln, err := net.Listen("tcp", ":"+port)
	if err != nil {
		log.Println(err)
		return
	}
	defer ln.Close()

	log.Println("|>>>>>>>>>>>>>>>|<<<<<<<<<<<<<<<|")
	log.Println("程序版本：" + version)
	log.Println("代理端口：" + port)
	log.Println("Key：" + key)
	log.Println("服务器地址：" + serverHost + ":" + serverPort)
	log.Println("|>>>>>>>>>>>>>>>|<<<<<<<<<<<<<<<|")

	s := &serve{
		serverHost: serverHost,
		serverPort: serverPort,
		key:        key,
		conf: &tls.Config{
			RootCAs: roots,
		},
	}

	//登录
	if err = s.handshake(); err != nil {
		log.Println("与服务器链接失败：", err)
		return
	}
	log.Println("登录成功,服务器连接完毕")

	for {
		conn, err := ln.Accept()
		if err != nil {
			log.Println(err)
			continue
		}
		go s.handleConnection(conn)
	}
}

func read(conn net.Conn) error {
	methods := make([]byte, 255)

	_, err := recv(methods[:2], 2, conn)
	if err != nil {
		return err
	}

	_, err = recv(methods[:], int(methods[1]), conn)
	if err != nil {
		return err
	}
	return nil
}

func recv(buf []byte, m int, conn net.Conn) (n int, err error) {
	for nn := 0; n < m; {
		nn, err = conn.Read(buf[n:m])
		if err != nil && err != io.EOF {
			return
		}
		n += nn
	}
	return
}

func encode(data interface{}) ([]byte, error) {
	buf := bytes.NewBuffer(nil)
	enc := gob.NewEncoder(buf)
	err := enc.Encode(data)
	if err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

type serve struct {
	serverHost string
	serverPort string
	key        string
	conf       *tls.Config
}

func (s *serve) handshake() error {
	//发送key登录
	pconn, ok, err := s.send(&Handshake{
		Type:  login,
		Value: map[string]string{"key": s.key},
	})
	if err != nil {
		return err
	}

	//登录失败
	if !ok {
		return errors.New("与服务器验证失败，请检查key是否正确")
	}
	//发送心跳包
	go func() {
		//心跳包发送间隔
		time.Sleep(time.Minute * 5)
		_, err := pconn.Write([]byte{0})
		if err != nil {
			pconn.Close()
			log.Println(err)
			return
		}
	}()
	return nil
}

//向服务器发送信息，返回信息为 建立的链接+是否操作成功+错误
func (s *serve) send(handshake *Handshake) (net.Conn, bool, error) {
	//建立链接
	pconn, err := tls.Dial("tcp", s.serverHost+":"+s.serverPort, s.conf)
	if err != nil {
		return nil, false, err
	}

	//编码
	enc, err := encode(handshake)
	if err != nil {
		pconn.Close()
		return nil, false, err
	}

	//发送信息
	_, err = pconn.Write(enc)
	if err != nil {
		pconn.Close()
		return nil, false, err
	}

	//读取服务端返回信息
	buf := make([]byte, 1)
	_, err = pconn.Read(buf)
	if err != nil {
		pconn.Close()
		return nil, false, err
	}

	//检查服务端是否返回操作成功
	if buf[0] != 0 {
		return pconn, false, nil
	}

	return pconn, true, nil
}

//处理浏览器发出的请求
func (s *serve) handleConnection(conn net.Conn) {
	log.Println("[+]", conn.RemoteAddr())

	//recv hello
	var err error
	err = read(conn)
	if err != nil {
		log.Println(err)
		conn.Close()
		return
	}

	//send echo
	buf := []byte{5, 0}
	_, err = conn.Write(buf)
	if err != nil {
		log.Println(err)
		conn.Close()
		return
	}

	var cmd cmd
	_, err = cmd.ReadFrom(conn)
	if err != nil {
		log.Println(err)
		conn.Close()
		return
	}

	if cmd.cmd != cmdConnect {
		log.Println("Error:", cmd.cmd)
		conn.Close()
		return
	}

	to := cmd.DestAddress()
	log.Println(conn.RemoteAddr(), "=="+cmd.reqtype+"=>", to)

	//与服务端建立链接
	pconn, ok, err := s.send(&Handshake{
		Type:  connection,
		Value: map[string]string{"reqtype": cmd.reqtype, "url": to},
	})
	if err != nil {
		conn.Close()
		log.Println("连接服务端失败：", err)
		return
	}

	//检查服务端是否返回成功
	if !ok {
		pconn.Close()
		conn.Close()
		log.Println("服务端验证失败，可能服务端已经重启，请重新登录")
		return
	}

	r := &cmdResp{
		ver: verSocks5,
		rep: repSucceeded,
		rsv: rsvReserved,
	}

	host, port, err := net.SplitHostPort(pconn.LocalAddr().String())
	if err != nil {
		log.Println(err)
		conn.Close()
		pconn.Close()
		return
	}

	ip := net.ParseIP(host)
	if ipv4 := ip.To4(); ipv4 != nil {
		r.atyp = atypIPv4Address
		r.bnd_addr = ipv4[:net.IPv4len]
	} else {
		r.atyp = atypIPv6Address
		r.bnd_addr = ip[:net.IPv6len]
	}

	prt, err := strconv.Atoi(port)
	if err != nil {
		log.Println(err)
		conn.Close()
		pconn.Close()
		return
	}
	r.bnd_port = uint16(prt)

	if _, err = r.WriteTo(conn); err != nil {
		log.Println(err)
		conn.Close()
		pconn.Close()
		return
	}

	go func(in net.Conn, out net.Conn, host, reqtype string) {
		io.Copy(in, out)
		in.Close()
		out.Close()
		log.Println(in.RemoteAddr(), "=="+reqtype+"=>", host, "[√]")
	}(conn, pconn, to, cmd.reqtype)

	go func(in net.Conn, out net.Conn, host, reqtype string) {
		io.Copy(in, out)
		in.Close()
		out.Close()
		log.Println(out.RemoteAddr(), "<="+reqtype+"==", host, "[√]")
	}(pconn, conn, to, cmd.reqtype)
}

type cmd struct {
	ver      byte
	cmd      byte
	rsv      byte
	atyp     byte
	reqtype  string
	dst_addr []byte
	dst_port uint16
}

func (c *cmd) DestAddress() string {
	var host string
	switch c.atyp {
	case atypIPv4Address:
		host = net.IPv4(c.dst_addr[0], c.dst_addr[1], c.dst_addr[2], c.dst_addr[3]).String()
	case atypDomainName:
		host = string(c.dst_addr)
	case atypIPv6Address:
		host = net.IP(c.dst_addr).String()
	default:
		host = "<unsupported address type>"
	}
	return host + ":" + strconv.Itoa(int(c.dst_port))
}
func (c *cmd) ReadFrom(conn net.Conn) (n int64, err error) {
	buf := make([]byte, 4)
	_, err = recv(buf, 4, conn)
	if err != nil {
		return
	}
	c.ver, c.cmd, c.rsv, c.atyp = buf[0], buf[1], buf[2], buf[3]

	switch c.cmd {
	case reqtypeTCP:
		c.reqtype = "tcp"
	case reqtypeBIND:
		log.Println("BIND")
	case reqtypeUDP:
		c.reqtype = "udp"
	}

	var ln byte
	switch c.atyp {
	case atypIPv4Address:
		ln = net.IPv4len
	case atypDomainName:
		err = binary.Read(io.Reader(conn), binary.BigEndian, &ln)
		if err != nil {
			return
		}
		n++
	case atypIPv6Address:
		ln = net.IPv6len
	default:
		return
	}
	c.dst_addr = make([]byte, ln)
	_, err = io.ReadFull(io.Reader(conn), c.dst_addr)
	if err != nil {
		return
	}
	n += int64(ln)

	err = binary.Read(io.Reader(conn), binary.BigEndian, &c.dst_port)
	if err != nil {
		return
	}
	n += 2
	return
}

type cmdResp struct {
	ver      byte
	rep      byte
	rsv      byte
	atyp     byte
	bnd_addr []byte
	bnd_port uint16
}

func (c *cmdResp) WriteTo(w io.Writer) (n int64, err error) {
	if c.ver != verSocks5 {
		err = errors.New("cmdResp.WriteTo: unsupported protocol version")
		return
	}
	buf := make([]byte, 0, net.IPv6len+8)
	buf = append(buf, c.ver, c.rep, c.rsv, c.atyp)
	switch c.atyp {
	case atypIPv4Address:
		if len(c.bnd_addr) < net.IPv4len {
			err = errors.New("cmdResp.bnd_addr too short")
			return
		}
		buf = append(buf, c.bnd_addr[:net.IPv4len]...)
	case atypDomainName:
		if len(c.bnd_addr) > 255 {
			err = errors.New("cmdResp.bnd_addr too large")
			return
		}
		buf = append(buf, byte(len(c.bnd_addr)))
		buf = append(buf, c.bnd_addr...)
	case atypIPv6Address:
		if len(c.bnd_addr) < net.IPv6len {
			err = errors.New("cmdResp.bnd_addr too short")
			return
		}
		buf = append(buf, c.bnd_addr[:net.IPv6len]...)
	}
	buf = append(buf, 0, 0)
	binary.BigEndian.PutUint16(buf[len(buf)-2:], c.bnd_port)
	var i int
	i, err = w.Write(buf)
	n = int64(i)
	return
}

type Handshake struct {
	Type  int
	Value map[string]string
}
