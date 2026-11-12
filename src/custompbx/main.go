package main

import (
	"bytes"
	"crypto/hmac"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha1" //nolint:gosec // Coturn REST credentials require HMAC-SHA1.
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"custompbx/cache"
	"custompbx/callwebhook"
	"custompbx/cfg"
	"custompbx/cweb"
	"custompbx/daemonCache"
	"custompbx/db"
	"custompbx/fsesl"
	"custompbx/hepHandler"
	"custompbx/logx"
	"custompbx/mainStruct"
	"custompbx/metrics"
	"custompbx/nocache"
	"custompbx/pbxcache"
	"custompbx/promexp"
	"custompbx/recordingapi"
	"custompbx/recordingstore"
	"custompbx/web"
	"custompbx/webcache"
	"custompbx/xmlcurl"
	"encoding/base64"
	"encoding/pem"
	"fmt"
	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/go-chi/render"
	"github.com/pion/turn/v4"
	"image"
	"image/gif"
	"image/jpeg"
	"image/png"
	"log/slog"
	"math/big"
	"mime"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

func main() {
	eventChannel := make(chan interface{}, 42)
	logsChannel := make(chan mainStruct.LogType, 420)

	logger.Info("CustomPBX starting", "version", mainStruct.Version)
	callwebhook.Start(cfg.CustomPbx.CallWebhook)
	daemonCache.InitDaemonState()
	logger.Info("starting database")
	db.StartDB()
	logger.Info("database connected")

	pbxcache.InitCacheObjects()
	webcache.InitCacheObjects()

	logger.Info("starting events handler")
	web.SetBroadcastChannel(eventChannel)
	go web.TimeEvents()

	fsesl.FirstConnectData()
	switchName := cache.GetCurrentInstanceName()
	if switchName != "" {
		if switchName != cfg.CustomPbx.Fs.Switchname {
			logger.Error("FreeSWITCH switchname differs from config file", "switchname", switchName, "configured", cfg.CustomPbx.Fs.Switchname)
			return
		}
	} else {
		if cfg.CustomPbx.Fs.Switchname == "" {
			logger.Error("FreeSWITCH switchname is not configured")
			return
		}
		switchName = cfg.CustomPbx.Fs.Switchname
		cache.SetCurrentInstanceName(cfg.CustomPbx.Fs.Switchname)
	}
	if switchName == "" {
		logger.Error("FreeSWITCH switchname is empty")
		return
	}
	logger.Info("FreeSWITCH switchname", "switchname", switchName)

	logger.Info("initialising cache")
	if daemonCache.State.DatabaseConnection {
		pbxcache.InitRootDB()
		db.InitCustomDB()
		logger.Info("checking DB migration")
		cache.InitCache()
		ok, err := db.Migrate(switchName)
		if err != nil {
			logger.Error("database schema update failed", "err", err)
		}
		if ok {
			logger.Info("database schema updated", "version", mainStruct.Version)
		}
		nocache.InitDB()
		db.InitLogDB()
		db.InitWebDB(cache.GetCurrentInstanceId())
		db.InitGlobalVariablesDB()
		nocache.InitServicesDB()

		pbxcache.InitPBXCache()
		webcache.InitUsersCache(cache.GetCurrentInstanceId())
		webcache.InitWebSettings(cache.GetCurrentInstanceId())
		webcache.InitWebData()
		if updated, err := fsesl.ApplyDemoCDRBootstrap(); err != nil {
			logger.Error("docker demo CDR bootstrap failed", "err", err)
		} else if updated {
			logger.Info("docker demo CDR database connection configured")
		}
		recordingstore.Start()

		logger.Info("starting ESL connection and handlers")
		go fsesl.ESLConnectKeeper(eventChannel, logsChannel)
		go metrics.UpdateMetrics()
		logger.Info("starting FreeSWITCH log collection")
		go freeswitchLogHandler(logsChannel)

		db.InitHEPDb()
		go func() { hepHandler.StartHepListener(db.SaveHEPPackets, cache.GetCurrentInstanceId()) }()
	} else {
		logger.Error("no service connections", "db", daemonCache.State.DatabaseConnection, "esl", daemonCache.State.ESLConnection)
	}

	promexp.SetCallbacks(
		func() bool { return daemonCache.State != nil && daemonCache.State.ESLConnection },
		func() bool { return daemonCache.State != nil && daemonCache.State.DatabaseConnection },
		func() (int, int) { return pbxcache.GetChannelsCache().Counters() },
	)
	// Private listener (not published by docker-compose); empty value disables it.
	go promexp.Serve(metricsListenAddr())

	logger.Info("starting web handlers")
	rCurl := chi.NewRouter()
	configureMiddleware(rCurl)
	rCurl.Post(cfg.CustomPbx.XMLCurl.Route, func(w http.ResponseWriter, r *http.Request) { dispatcher(w, r, eventChannel) })

	rWeb := chi.NewRouter()
	configureMiddleware(rWeb)
	rWeb.Get(cfg.CustomPbx.Web.Route, web.StartWS)
	rWeb.Get("/api/v1/ws/metrics", web.HubMetrics)
	rWeb.Get("/api/v1/pbx/recordings/{uuid}", func(w http.ResponseWriter, r *http.Request) {
		recordingapi.Serve(w, r, chi.URLParam(r, "uuid"))
	})
	rWeb.Post("/api/v1", web.PostAPIRequest)
	configureStaticRoutes(rWeb)

	go turnServer()
	startServers(rWeb, rCurl)
}

func metricsListenAddr() string {
	if v, ok := os.LookupEnv("CUSTOMPBX_METRICS_ADDR"); ok {
		return strings.TrimSpace(v)
	}
	return ":9102"
}

func startServers(r chi.Router, cr chi.Router) {
	curlCert, curlKey, webCert, webKey := checkAndCreateCerts()

	if cfg.CustomPbx.XMLCurl.Secure {
		logger.Info("starting XMLCurl server", "secure", true)
		go func() {
			logx.Fatal(logger, "XMLCurl server stopped", "err", http.ListenAndServeTLS(cfg.CustomPbx.XMLCurl.Host+":"+strconv.Itoa(cfg.CustomPbx.XMLCurl.Port), curlCert, curlKey, cr))
		}()
	} else {
		logger.Info("starting XMLCurl server", "secure", false)
		go func() {
			logx.Fatal(logger, "XMLCurl server stopped", "err", http.ListenAndServe(cfg.CustomPbx.XMLCurl.Host+":"+strconv.Itoa(cfg.CustomPbx.XMLCurl.Port), cr))
		}()
	}

	if cfg.CustomPbx.Web.Secure {
		logger.Info("starting web server", "secure", true)
		logx.Fatal(logger, "web server stopped", "err", http.ListenAndServeTLS(cfg.CustomPbx.Web.Host+":"+strconv.Itoa(cfg.CustomPbx.Web.Port), webCert, webKey, r))
	} else {
		logger.Info("starting web server", "secure", false)
		logx.Fatal(logger, "web server stopped", "err", http.ListenAndServe(cfg.CustomPbx.Web.Host+":"+strconv.Itoa(cfg.CustomPbx.Web.Port), r))
	}
}

func configureMiddleware(r chi.Router) {
	r.Use(middleware.RequestID)
	r.Use(middleware.RealIP)
	r.Use(requestLogger)
	r.Use(middleware.Recoverer)
	r.Use(render.SetContentType(render.ContentTypeJSON))
	r.Use(middleware.Timeout(60 * time.Second))
}

func configureStaticRoutes(r chi.Router) {
	// Define other routes here
	r.Route("/cweb", func(r chi.Router) {
		r.Get("/{any1}/{any2}/{any3}/{any4}/{any5}/{any6}/{any7}/{any8}/{any9}", Web)
		r.Get("/{any1}/{any2}/{any3}/{any4}/{any5}/{any6}/{any7}/{any8}", Web)
		r.Get("/{any1}/{any2}/{any3}/{any4}/{any5}/{any6}/{any7}", Web)
		r.Get("/{any1}/{any2}/{any3}/{any4}/{any5}/{any6}", Web)
		r.Get("/{any1}/{any2}/{any3}/{any4}/{any5}", Web)
		r.Get("/{any1}/{any2}/{any3}/{any4}", Web)
		r.Get("/{any1}/{any2}/{any3}", Web)
		r.Get("/{any1}/{any2}", Web)
		r.Get("/{any1}", Web)
		r.Get("/", Web)
		//r.Get("/reload/callcenter", ReloadCallcenter)

		/*	r.Get("/assets/sounds/{sounds}", Web)
			r.Get("/assets/img/{img}", Web)
			r.Get("/{file}", Web)*/
	})
}

func dispatcher(w http.ResponseWriter, r *http.Request, events chan interface{}) {
	defer func() {
		if r := recover(); r != nil {
			logger.Error("recovered panic in dispatcher", "panic", fmt.Sprint(r))
		}
	}()
	var response []byte

	var Header = `<?xml version="1.0" encoding="UTF-8" standalone="no"?>` + "\n"
	err := r.ParseForm()
	if err != nil {
		logger.Error("dispatcher failed", "err", err)
		response = xmlcurl.NotFound(make(map[string]string))
		w.Header().Set("Content-Type", "application/xml")
		w.Write(append([]byte(Header), response...))
		return
	}

	form := make(map[string]string)
	for key := range r.PostForm {
		form[key] = r.PostFormValue(key)
	}
	switch r.FormValue("section") {
	case "directory":
		response = xmlcurl.Directory(form)
	case "configuration":
		response = xmlcurl.Configuration(form)
	case "dialplan":
		response = xmlcurl.Dialplan(form, events)
	default:
		response = xmlcurl.NotFound(form)
	}
	w.Header().Set("Content-Type", "application/xml")
	w.Write(append([]byte(Header), response...))
}

func Web(rw http.ResponseWriter, r *http.Request) {
	var err error
	defer r.Body.Close()

	route1 := chi.URLParam(r, "any1")
	route2 := chi.URLParam(r, "any2")
	route3 := chi.URLParam(r, "any3")
	route4 := chi.URLParam(r, "any4")
	route5 := chi.URLParam(r, "any5")
	route6 := chi.URLParam(r, "any6")
	route7 := chi.URLParam(r, "any7")
	route8 := chi.URLParam(r, "any8")
	route9 := chi.URLParam(r, "any9")
	filePath := "index.html"
	if route1 != "" {
		filePath = route1
	}
	if route2 != "" {
		filePath = filePath + "/" + route2
	}
	if route3 != "" {
		filePath = filePath + "/" + route3
	}
	if route4 != "" {
		filePath = filePath + "/" + route4
	}

	var file []byte
	if route1 == "assets" && route2 == "img" && route3 == "avatar" {
		requester, status := web.UserFromCookie(r, "token")
		if status != http.StatusOK || requester.Login == "" {
			http.Error(rw, "access denied", status)
			return
		}

		extension := filepath.Ext(route4)
		userId := route4[0 : len(route4)-len(extension)]
		id, err := strconv.ParseInt(userId, 10, 64)
		if err != nil {
			return
		}
		user := webcache.GetWebUserById(id)
		if user == nil {
			return
		}
		unbased, _ := base64.StdEncoding.DecodeString(user.Avatar)
		var img image.Image
		buf := new(bytes.Buffer)
		switch user.AvatarFormat {
		case "jpeg":
			img, err = jpeg.Decode(bytes.NewReader(unbased))
			if err != nil {
				return
			}
			err = jpeg.Encode(buf, img, nil)
			if err != nil {
				return
			}
			file = buf.Bytes()
		case "png":
			img, err = png.Decode(bytes.NewReader(unbased))
			if err != nil {
				return
			}
			err = png.Encode(buf, img)
			if err != nil {
				return
			}
			file = buf.Bytes()
		case "gif":
			img, err = gif.Decode(bytes.NewReader(unbased))
			if err != nil {
				return
			}
			err = gif.Encode(buf, img, nil)
			if err != nil {
				return
			}
			file = buf.Bytes()
		}
	} else if route1 == "cdr" && route2 == "records" {
		requester, status := web.UserFromCookie(r, "token")
		if status != http.StatusOK || requester.Login == "" {
			http.Error(rw, "access denied", status)
			return
		}

		if route3 != "" {
			objectKey := strings.Join([]string{route3, route4, route5, route6, route7, route8, route9}, "/")
			objectKey = strings.TrimRight(objectKey, "/")
			if recordingstore.Enabled() && strings.HasPrefix(objectKey, "pbx/recordings/") {
				object, info, objectErr := recordingstore.ServeHTTP(r.Context(), objectKey)
				if objectErr != nil {
					http.Error(rw, "recording not found", http.StatusNotFound)
					return
				}
				defer object.Close()
				rw.Header().Set("Content-Type", "audio/wav")
				rw.Header().Set("X-Content-Type-Options", "nosniff")
				http.ServeContent(rw, r, filepath.Base(objectKey), info.LastModified, object)
				return
			}
			servePath := webcache.GetWebSetting(webcache.CdrFileServerPath)
			if servePath != "" && servePath != "/" {
				servePath, err = resolveFileUnderRoot(servePath, route3, route4, route5, route6, route7, route8, route9)
				if err != nil {
					http.Error(rw, "not found", http.StatusNotFound)
					return
				}
				http.ServeFile(rw, r, servePath)

				return
				/*				file, err = ioutil.ReadFile(servePath)
								if err != nil {
									file = []byte("not found")
								}
								rw.Header().Set("accept-ranges", "bytes")*/
			} else {
				file = []byte("cdr path is filesystem root")
			}
		} else {
			file = []byte("no route")
		}
	}

	path := r.URL.Path[1:]
	// set content-type and content-encoding
	rw.Header().Set("Content-Type", mime.TypeByExtension(filepath.Ext(path))+"; charset=utf-8")
	//rw.Header().Set("Content-Encoding", "gzip")

	// security headers
	// SecurityHeaders(rw, r)
	// gzip compression
	//w, _ := gzip.NewWriterLevel(rw, 2)
	//defer w.Close()

	if file == nil {
		file, err = cweb.Asset(filePath)
		if err != nil {
			logger.Error("Web failed", "err", err)
			file = cweb.MustAsset("index.html")
		}
	}

	// write file
	_, err = rw.Write(file)
	if err != nil {
		// print unknown error
		logger.Error("Web failed", "err", err)
	}
}

func resolveFileUnderRoot(root string, segments ...string) (string, error) {
	root, err := filepath.Abs(filepath.Clean(root))
	if err != nil {
		return "", err
	}
	parts := []string{root}
	for _, segment := range segments {
		if segment == "" {
			continue
		}
		if filepath.IsAbs(segment) || segment == "." || segment == ".." || strings.ContainsAny(segment, `/\\`) {
			return "", os.ErrNotExist
		}
		parts = append(parts, segment)
	}
	candidate, err := filepath.Abs(filepath.Join(parts...))
	if err != nil {
		return "", err
	}
	rel, err := filepath.Rel(root, candidate)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(os.PathSeparator)) {
		return "", os.ErrNotExist
	}
	info, err := os.Stat(candidate)
	if err != nil || !info.Mode().IsRegular() {
		return "", os.ErrNotExist
	}
	return candidate, nil
}

func SecurityHeaders(w http.ResponseWriter, r *http.Request) {
	// content-security-policy
	w.Header().Set("Content-Security-Policy",
		"default-src 'self'; img-src 'self' data:; script-src 'self' 'unsafe-inline'; style-src 'self' 'unsafe-inline';")

	// access-control-allow-origin
	if origin := r.Header.Get("Origin"); origin != "" {
		w.Header().Set("Access-ConfigConferenceCallerControlsControls-Allow-Origin", origin)
	}

	// access-control-allow-headers
	w.Header().Set("Access-ConfigConferenceCallerControlsControls-Allow-Headers",
		"Accept, Content-Type, Content-Length, Accept-Encoding, X-CSRF-Token, Authorization")

	// access-control-allow-methods
	w.Header().Set("Access-ConfigConferenceCallerControlsControls-Allow-Methods", "POST")
}

func freeswitchLogHandler(logsChannel chan mainStruct.LogType) {
	chuck := 300
	collector := make([]mainStruct.LogType, 0, chuck)
	tick := time.Tick(300 * time.Millisecond)

	for {
		select {
		case logLine := <-logsChannel:
			collector = append(collector, logLine)
			if len(collector) < chuck {
				continue
			}
			go db.SetLogLines(collector, cache.GetCurrentInstanceId())
			collector = []mainStruct.LogType{}
		case <-tick:
			if len(collector) == 0 {
				continue
			}
			go db.SetLogLines(collector, cache.GetCurrentInstanceId())
			collector = []mainStruct.LogType{}
		}
	}
}

func turnServer() {
	if cfg.CustomPbx.Web.StunPort < 3000 || cfg.CustomPbx.Web.StunPort > 65765 {
		logger.Error("STUN/TURN: stun port not in range 3000-65535")
		daemonCache.State.StunServerStatus = false
		return
	}
	udpListener, err := net.ListenPacket("udp4", "0.0.0.0:"+strconv.Itoa(cfg.CustomPbx.Web.StunPort))
	if err != nil {
		logger.Error("STUN/TURN: failed to create TURN server listener", "err", err)
		return
	}

	realm := strings.TrimSpace(os.Getenv("PBX_TURN_REALM"))
	if realm == "" {
		realm = "custom-pbx.com"
	}
	sharedSecret := strings.TrimSpace(os.Getenv("PBX_TURN_SHARED_SECRET"))
	relayAddress := strings.TrimSpace(os.Getenv("PBX_TURN_EXTERNAL_IP"))
	if relayAddress == "" {
		relayAddress = cfg.CustomPbx.Web.Host
	}
	relayIP := net.ParseIP(relayAddress)
	if relayIP == nil {
		logger.Error("STUN/TURN: invalid PBX_TURN_EXTERNAL_IP", "value", relayAddress)
		daemonCache.State.StunServerStatus = false
		return
	}
	if relayIP.IsUnspecified() {
		logger.Warn("STUN/TURN: PBX_TURN_EXTERNAL_IP is not configured; STUN works but relayed media will advertise an unusable address")
	}
	tcpListener, err := net.Listen("tcp4", "0.0.0.0:"+strconv.Itoa(cfg.CustomPbx.Web.StunPort))
	if err != nil {
		logger.Error("STUN/TURN: failed to create TCP listener", "err", err)
		daemonCache.State.StunServerStatus = false
		_ = udpListener.Close()
		return
	}

	listenerConfigs := []turn.ListenerConfig{
		{
			Listener:              tcpListener,
			RelayAddressGenerator: newTURNRelayAddressGenerator(relayIP),
		},
	}
	tlsPort := 0
	tlsCertFile := strings.TrimSpace(os.Getenv("PBX_TURN_TLS_CERT_FILE"))
	tlsKeyFile := strings.TrimSpace(os.Getenv("PBX_TURN_TLS_KEY_FILE"))
	if tlsCertFile != "" || tlsKeyFile != "" {
		if tlsCertFile == "" || tlsKeyFile == "" {
			logger.Error("STUN/TURN: both PBX_TURN_TLS_CERT_FILE and PBX_TURN_TLS_KEY_FILE are required")
			daemonCache.State.StunServerStatus = false
			_ = udpListener.Close()
			_ = tcpListener.Close()
			return
		}

		tlsPort, err = turnTLSPort(os.Getenv("PBX_TURN_TLS_PORT"))
		if err != nil {
			logger.Error("STUN/TURN: TLS configuration failed", "err", err)
			daemonCache.State.StunServerStatus = false
			_ = udpListener.Close()
			_ = tcpListener.Close()
			return
		}
		certificate, loadErr := tls.LoadX509KeyPair(tlsCertFile, tlsKeyFile)
		if loadErr != nil {
			logger.Error("STUN/TURN: failed to load TLS certificate", "err", loadErr)
			daemonCache.State.StunServerStatus = false
			_ = udpListener.Close()
			_ = tcpListener.Close()
			return
		}
		tlsListener, listenErr := tls.Listen("tcp4", "0.0.0.0:"+strconv.Itoa(tlsPort), &tls.Config{
			MinVersion:   tls.VersionTLS12,
			Certificates: []tls.Certificate{certificate},
		})
		if listenErr != nil {
			logger.Error("STUN/TURN: failed to create TLS listener", "err", listenErr)
			daemonCache.State.StunServerStatus = false
			_ = udpListener.Close()
			_ = tcpListener.Close()
			return
		}
		listenerConfigs = append(listenerConfigs, turn.ListenerConfig{
			Listener:              tlsListener,
			RelayAddressGenerator: newTURNRelayAddressGenerator(relayIP),
		})
	}

	_, err = turn.NewServer(turn.ServerConfig{
		// Keep ChannelBindTimeout unset so Pion uses the TURN protocol default
		// lifetime (10 minutes). Browsers refresh channel bindings on that
		// cadence; overriding it with a very short lifetime causes one-way audio.
		Realm: realm,
		AuthHandler: func(username string, realm string, srcAddr net.Addr) ([]byte, bool) {
			return temporaryTURNAuthKey(sharedSecret, username, realm, time.Now())
		},
		PacketConnConfigs: []turn.PacketConnConfig{
			{
				PacketConn:            udpListener,
				RelayAddressGenerator: newTURNRelayAddressGenerator(relayIP),
			},
		},
		ListenerConfigs: listenerConfigs,
	})
	if err != nil {
		logger.Error("STUN server failed", "err", err)
		daemonCache.State.StunServerStatus = false
		return
	}

	if tlsPort == 0 {
		logger.Info("STUN/TURN server listening", "port", cfg.CustomPbx.Web.StunPort, "tls", false)
	} else {
		logger.Info("STUN/TURN server listening", "port", cfg.CustomPbx.Web.StunPort, "tls_port", tlsPort, "tls", true)
	}
	daemonCache.State.StunServerStatus = true
	sigs := make(chan interface{}, 1)
	<-sigs

	daemonCache.State.StunServerStatus = false
}

func newTURNRelayAddressGenerator(relayIP net.IP) *turn.RelayAddressGeneratorPortRange {
	return &turn.RelayAddressGeneratorPortRange{
		RelayAddress: relayIP,
		Address:      "0.0.0.0",
		MinPort:      49160,
		MaxPort:      49200,
	}
}

func turnTLSPort(value string) (int, error) {
	if strings.TrimSpace(value) == "" {
		return 5349, nil
	}

	port, err := strconv.Atoi(value)
	if err != nil || port < 1 || port > 65535 {
		return 0, fmt.Errorf("invalid PBX_TURN_TLS_PORT %q", value)
	}
	return port, nil
}

func temporaryTURNAuthKey(sharedSecret, username, realm string, now time.Time) ([]byte, bool) {
	if sharedSecret == "" {
		return nil, false
	}

	expiresAt, _, found := strings.Cut(username, ":")
	if !found {
		return nil, false
	}
	expiry, err := strconv.ParseInt(expiresAt, 10, 64)
	if err != nil || expiry <= now.Unix() {
		return nil, false
	}

	mac := hmac.New(sha1.New, []byte(sharedSecret))
	_, _ = mac.Write([]byte(username))
	password := base64.StdEncoding.EncodeToString(mac.Sum(nil))
	return turn.GenerateAuthKey(username, realm, password), true
}

func checkAndCreateCerts() (string, string, string, string) {
	var curlCert = cfg.CustomPbx.XMLCurl.CertPath
	var curlKey = cfg.CustomPbx.XMLCurl.KeyPath
	var webCert = cfg.CustomPbx.Web.CertPath
	var webKey = cfg.CustomPbx.Web.KeyPath

	var err error
	logger.Info("checking certs")
	curlCert, curlKey, err = loadCertificateAndKey(curlCert, curlKey)
	if err != nil {
		logger.Warn("certificate check failed, falling back to self-signed", "err", err)
		curlCert = "./cert.pem"
		curlKey = curlCert
		_, err := os.ReadFile(curlCert)
		if err != nil {
			createCert(curlCert)
		}
	}

	if cfg.CustomPbx.XMLCurl.CertPath == cfg.CustomPbx.Web.CertPath {
		return curlCert, curlKey, curlCert, curlKey
	}

	logger.Info("checking web cert", "cert", webCert, "key", webKey)
	webCert, webKey, err = loadCertificateAndKey(webCert, webKey)
	if err != nil {
		logger.Warn("certificate check failed, falling back to self-signed", "err", err)
		webCert = "./cert.pem"
		webKey = webCert
		_, err := os.ReadFile(webCert)
		if err != nil {
			createCert(webCert)
		}
	}

	return curlCert, curlKey, webCert, webKey
}

func createCert(filePath string) {
	// Generate a new private key
	privateKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		logger.Error("failed to generate private key", "err", err)
		return
	}

	// Create a template for the certificate
	template := x509.Certificate{
		SerialNumber:          big.NewInt(1),
		Subject:               pkix.Name{CommonName: "Self-Signed Certificate"},
		NotBefore:             time.Now(),
		NotAfter:              time.Now().AddDate(1, 0, 0), // Valid for 1 year
		IsCA:                  true,
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageDigitalSignature,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth, x509.ExtKeyUsageClientAuth},
		BasicConstraintsValid: true,
	}

	// Generate the certificate using the template and the private key
	derBytes, err := x509.CreateCertificate(rand.Reader, &template, &template, &privateKey.PublicKey, privateKey)
	if err != nil {
		logger.Error("failed to create certificate", "err", err)
		return
	}

	// Create a buffer to hold the PEM data
	pemBuffer := []byte{}

	// Append the private key PEM block to the buffer
	privateKeyPEM := &pem.Block{
		Type:  "RSA PRIVATE KEY",
		Bytes: x509.MarshalPKCS1PrivateKey(privateKey),
	}
	pemBuffer = append(pemBuffer, pem.EncodeToMemory(privateKeyPEM)...)

	// Append the certificate PEM block to the buffer
	certPEM := &pem.Block{
		Type:  "CERTIFICATE",
		Bytes: derBytes,
	}
	pemBuffer = append(pemBuffer, pem.EncodeToMemory(certPEM)...)

	// Save the combined PEM data to a file
	combinedFile, err := os.Create(filePath)
	if err != nil {
		logger.Error("failed to create PEM file", "path", filePath, "err", err)
		return
	}
	defer combinedFile.Close()

	if _, err := combinedFile.Write(pemBuffer); err != nil {
		logger.Error("failed to create PEM file", "path", filePath, "err", err)
		return
	}

	logger.Info("self-signed certificate and private key generated", "path", filePath)
}

func loadCertificateAndKey(certFilePath, keyFilePath string) (string, string, error) {
	certBytes, err := os.ReadFile(certFilePath)
	if err != nil {
		return "", "", err
	}

	// Decode the PEM data and get the certificate and private key
	var cert *x509.Certificate
	//var privateKey interface{}
	var certContainsKey bool

	for {
		block, rest := pem.Decode(certBytes)
		if block == nil {
			break
		}

		certBytes = rest

		switch block.Type {
		case "CERTIFICATE":
			cert, err = x509.ParseCertificate(block.Bytes)
			if err != nil {
				return "", "", err
			}
		case "RSA PRIVATE KEY", "PRIVATE KEY":
			_, err = x509.ParsePKCS1PrivateKey(block.Bytes)
			if err != nil {
				// If PKCS#1 parsing fails, try PKCS#8 parsing
				_, err = x509.ParsePKCS8PrivateKey(block.Bytes)
				if err != nil {
					return "", "", err
				}
			}
			certContainsKey = true
		default:
			// Handle other PEM blocks if needed
		}
	}

	keyFile := certFilePath
	// If the certificate doesn't contain the key, load the key from the key file
	if !certContainsKey {
		keyFile = keyFilePath
		privateKeyBytes, err := os.ReadFile(keyFilePath)
		if err != nil {
			return "", "", err
		}

		_, err = x509.ParsePKCS8PrivateKey(privateKeyBytes)
		if err != nil {
			return "", "", err
		}
	}

	logger.Info("certificate is valid",
		"cert", certFilePath, "key", keyFile,
		"subject", cert.Subject.CommonName, "issuer", cert.Issuer.CommonName,
		"not_before", cert.NotBefore, "not_after", cert.NotAfter,
	)

	return certFilePath, keyFile, nil
}

// requestLogger replaces chi's middleware.Logger (which wrote unstructured
// text through the std logger) with a structured slog access log.
// Successful requests are Debug (XML-CURL is polled constantly); 5xx are
// Error and 4xx Warn.
func requestLogger(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ww := middleware.NewWrapResponseWriter(w, r.ProtoMajor)
		start := time.Now()
		next.ServeHTTP(ww, r)
		status := ww.Status()
		if status == 0 {
			status = http.StatusOK
		}
		level := slog.LevelDebug
		switch {
		case status >= 500:
			level = slog.LevelError
		case status >= 400:
			level = slog.LevelWarn
		}
		logger.Log(r.Context(), level, "http request",
			"method", r.Method, "path", r.URL.Path, "status", status,
			"bytes", ww.BytesWritten(), "duration_ms", time.Since(start).Milliseconds(),
			"remote_addr", r.RemoteAddr, "request_id", middleware.GetReqID(r.Context()))
	})
}
