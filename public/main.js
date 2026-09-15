const localVideo = document.getElementById("localVideo")
const remoteVideo = document.getElementById("remoteVideo")
const statusMessage = document.getElementById("statusMessage")
const qualityControl = document.getElementById("qualityControl")
const toggleMicBtn = document.getElementById("toggleMicBtn")
const toggleVideoBtn = document.getElementById("toggleVideoBtn")
const remoteVolume = document.getElementById("remoteVolume")
const localVolume = document.getElementById("localVolume")
const toggleScreenBtn = document.getElementById("toggleScreenBtn")
const toggleRecordBtn = document.getElementById("toggleRecordBtn")
const chatMessages = document.getElementById("chatMessages")
const chatInput = document.getElementById("chatInput")
const sendChatBtn = document.getElementById("sendChatBtn")

const chatFileInput = document.getElementById("chatFileInput")
const attachFileBtn = document.getElementById("attachFileBtn")
const typingIndicator = document.getElementById("typingIndicator")

let localStream = null;
let videoSender = null; // Kameraya ait RTCRtpSender referansı - kapat/aç sırasında kullanılıyor

const audioContext = new AudioContext();
const gainNode = audioContext.createGain();
gainNode.gain.value = 1;
const remoteGainNode = audioContext.createGain();
remoteGainNode.gain.value = 1;


let peerConnection;

async function initializeWebRTC() {
    try {
        const response = await fetch('/api/turn-credentials');
        const data = await response.json();

        const iceServers = Array.isArray(data.iceServers) ? data.iceServers : [];
        if (data.success) {
            console.log("TURN bilgileri başarıyla alındı.");
        } else {
            console.warn("TURN bilgileri alınamadı, sadece STUN ile devam ediliyor.");
        }

        peerConnection = new RTCPeerConnection({ 
            iceServers, 
            //iceTransportPolicy: 'relay'
        });

        peerConnection.onicecandidate = (event) => {
            console.log("onicecandidate tetiklendi, candidate:", event.candidate);
            if (event.candidate) {
                ws.send(JSON.stringify({
                    type: "candidate",
                    room: roomId,
                    payload: event.candidate
                }));
            }
        };

        peerConnection.ontrack = (event) => {
            remoteVideo.srcObject = event.streams[0];
            const remoteSource = audioContext.createMediaStreamSource(event.streams[0]);
            remoteSource.connect(remoteGainNode);
            remoteGainNode.connect(audioContext.destination);
        };

        console.log("WebRTC bağlantısı (TURN dahil) başlatıldı.");

    } catch (error) {
        console.error("TURN sunucusu hazırlanamadı, STUN ile devam ediliyor:", error);
        peerConnection = new RTCPeerConnection({ iceServers: [] });
    }
}

const rtcReady = initializeWebRTC();

qualityControl.addEventListener("change", async (event) => {
    const selectQuality = event.target.value;

    let targetWidth = 640;
    let targetHeight = 480;
    let targetBitrate = 300 * 1000;

    if (selectQuality === "480") {
        targetWidth = 640;
        targetHeight = 480;
        targetBitrate = 300 * 1000;
    }
    else if (selectQuality === "720") {
        targetWidth = 1280;
        targetHeight = 720;
        targetBitrate = 800 * 1000;
    }
    else if (selectQuality === "1080") {
        targetWidth = 1920;
        targetHeight = 1080;
        targetBitrate = 1500 * 1000;
    }

    try {
        const videoTrack = localVideo.srcObject.getVideoTracks()[0];
        await videoTrack.applyConstraints({
            width: { ideal: targetWidth },
            height: { ideal: targetHeight }
        });
        console.log("Gerçek ayarlar:", videoTrack.getSettings());

        await rtcReady;
        const senders = peerConnection.getSenders();
        const videoSender = senders.find(s => s.track && s.track.kind == "video");

        if (videoSender) {
            const parameters = videoSender.getParameters();
            if (!parameters.encodings) {
                parameters.encodings = [{}];
            }
            parameters.encodings[0].maxBitrate = targetBitrate;
            await videoSender.setParameters(parameters);
        }

        console.log("Video kalitesi ayarlandı:", selectQuality);

    } catch (error) {
        console.log("Kalite değistirme başarısız oldu:", error);
        alert("Kameranız seçtiğiniz kaliteyi desteklemiyor olabilir.");
    }
});

toggleVideoBtn.addEventListener("click", async () => {
    // Zaten bir video track'imiz varsa (kamerayla katıldıysak), sadece aç/kapat.
    const existingTrack = localStream && localStream.getVideoTracks()[0];
    if (existingTrack) {
        const turningOn = !existingTrack.enabled;
        existingTrack.enabled = turningOn;
        toggleVideoBtn.textContent = turningOn ? "Kamerayı kapat" : "Kamerayı aç";

        // Sadece "enabled" değiştirmek bazı tarayıcılarda karşı tarafta
        // görüntünün donuk/siyah kalmasına sebep oluyor. Bunu önlemek için
        // gönderilen track'i de null <-> gerçek track olarak değiştiriyoruz;
        // bu karşı tarafta görüntünün gerçekten tazelenmesini sağlıyor.
        if (videoSender) {
            try {
                await videoSender.replaceTrack(turningOn ? existingTrack : null);
            } catch (err) {
                console.error("Video track değiştirilemedi:", err);
            }
        }
        return;
    }

    // Kamerayla katılmadıysak: şimdi izin isteyip görüşmeye ekle.
    if (isScreenSharing) return; // ekran paylaşırken kamerayı sonradan eklemeyelim

    try {
        toggleVideoBtn.disabled = true;
        const camStream = await navigator.mediaDevices.getUserMedia({ video: true });
        const videoTrack = camStream.getVideoTracks()[0];

        if (!localStream) {
            localStream = new MediaStream();
        }
        localStream.addTrack(videoTrack);
        localVideo.srcObject = localStream;

        await rtcReady;
        videoSender = peerConnection.addTrack(videoTrack, localStream);
        const parameters = videoSender.getParameters();
        if (!parameters.encodings) parameters.encodings = [{}];
        parameters.encodings[0].maxBitrate = 500 * 1000;
        await videoSender.setParameters(parameters);

        await renegotiate();

        toggleVideoBtn.textContent = "Kamerayı kapat";
    } catch (err) {
        console.error("Kamera sonradan açılamadı:", err);
        alert("Kameraya erişim sağlanamadı. Tarayıcı izinlerini kontrol edin.");
    } finally {
        toggleVideoBtn.disabled = false;
    }
})

toggleMicBtn.addEventListener("click", async () => {
    // Zaten bir ses track'imiz varsa (mikrofonla katıldıysak), sadece aç/kapat.
    const existingTrack = localStream && localStream.getAudioTracks()[0];
    if (existingTrack) {
        existingTrack.enabled = !existingTrack.enabled;
        toggleMicBtn.textContent = existingTrack.enabled ? "Mikrofunu kapat" : "Mikrofonu aç";
        return;
    }

    // Mikrofonla katılmadıysak: şimdi izin isteyip görüşmeye ekle.
    try {
        toggleMicBtn.disabled = true;
        const micStream = await navigator.mediaDevices.getUserMedia({ audio: true });
        const rawAudioTrack = micStream.getAudioTracks()[0];

        // Ses seviyesi kontrolünün (gainNode) çalışması için, mikrofon
        // kamerayla katılırken de olduğu gibi gainNode üzerinden geçiriliyor.
        const micSource = audioContext.createMediaStreamSource(new MediaStream([rawAudioTrack]));
        const destination = audioContext.createMediaStreamDestination();
        micSource.connect(gainNode);
        gainNode.connect(destination);
        const processedAudioTrack = destination.stream.getAudioTracks()[0];

        if (!localStream) {
            localStream = new MediaStream();
        }
        localStream.addTrack(rawAudioTrack);
        localVideo.srcObject = localStream;

        await rtcReady;
        peerConnection.addTrack(processedAudioTrack, localStream);

        await renegotiate();

        toggleMicBtn.textContent = "Mikrofunu kapat";
    } catch (err) {
        console.error("Mikrofon sonradan açılamadı:", err);
        alert("Mikrofona erişim sağlanamadı. Tarayıcı izinlerini kontrol edin.");
    } finally {
        toggleMicBtn.disabled = false;
    }
})

const urlParams = new URLSearchParams(window.location.search)
let roomId = urlParams.get("room")
let myName = urlParams.get("name") || "Misafir"

if (!roomId) {
    window.location.href = "/";
}

const wsProtocol = window.location.protocol === "https:" ? "wss:" : "ws:";

// Kamera/mikrofon ve sinyal bağlantısı artık sayfa açılır açılmaz değil,
// kullanıcı "Görüşmeye Katıl" butonuna bastığında başlatılıyor.
let ws;
let cameraReady;
let intentionalClose = false;

const preJoin = document.getElementById("preJoin");
const callUI = document.getElementById("callUI");
const joinCallBtn = document.getElementById("joinCallBtn");
const joinWithVideo = document.getElementById("joinWithVideo");
const joinWithAudio = document.getElementById("joinWithAudio");

joinCallBtn.addEventListener("click", async () => {
    joinCallBtn.disabled = true;
    statusMessage.textContent = "Kamera/mikrofon erişimi isteniyor...";

    cameraReady = startCamera(joinWithVideo.checked, joinWithAudio.checked);
    await cameraReady;

    preJoin.style.display = "none";
    callUI.style.display = "block";
    statusMessage.textContent = "";

    toggleVideoBtn.textContent = joinWithVideo.checked ? "Kamerayı kapat" : "Kamerayı aç";
    toggleMicBtn.textContent = joinWithAudio.checked ? "Mikrofunu kapat" : "Mikrofonu aç";

    connectSignaling();
});

function connectSignaling() {
    ws = new WebSocket(`${wsProtocol}//${window.location.host}/ws`);

    ws.onopen = () => {
        console.log("Signaling server \'a bağlanıldı");
        ws.send(JSON.stringify({ type: "join", room: roomId, payload: null }));
    };

    window.addEventListener('beforeunload', function () {
        if (ws && ws.readyState === WebSocket.OPEN) {
            ws.close();
        }
    });

    ws.onmessage = handleSignalingMessage;
    ws.onerror = handleSignalingError;
    ws.onclose = handleSignalingClose;
}

async function handleSignalingMessage(event) {
    const msg = JSON.parse(event.data);
    console.log("Mesaj alındı,tip:", msg.type);

    if (msg.type === "offer") {
        console.log("Offer işleniyor...");
        await rtcReady;

        await peerConnection.setRemoteDescription(msg.payload)
        const answer = await peerConnection.createAnswer();
        await peerConnection.setLocalDescription(answer);

        ws.send(JSON.stringify({
            type: "answer",
            room: roomId,
            payload: answer,
        }));
    } else if (msg.type === "answer") {
        await rtcReady;
        await peerConnection.setRemoteDescription(msg.payload)
    } else if (msg.type === "candidate") {
        try {
            await rtcReady;
            await peerConnection.addIceCandidate(msg.payload)
            console.log("hata yok (candidate eklendi)")
        } catch (error) {
            console.error("hata: candidate eklenirken catch içine düştü", error)
        }
    }
    else if (msg.type === "ready") {
        console.log("Ready alındı, offer başlatılıyor");
        await cameraReady;
        await rtcReady;
        await createAndSendOffer();
        console.log("Offer gönderildi");
    }
    else if (msg.type === "peer-left") {
        remoteVideo.srcObject = null;
        console.log("Karşı taraf ayrıldı");
    }
    else if (msg.type === "room-full") {
        statusMessage.textContent = "Bu oda dolu (2 kişi sınırı). Lütfen farklı bir link kullanın.";
        intentionalClose = true;
        ws.close();
    }
    else if (msg.type === "room-not-found") {
        statusMessage.textContent = "Bu oda geçersiz veya artık kullanılamıyor. Ana sayfaya yönlendiriliyorsunuz...";
        intentionalClose = true;
        ws.close();
        setTimeout(() => {
            window.location.href = "/";
        }, 3000);
    }
    else if (msg.type === "chat") {
        appendChatMessage(msg.payload, false);
    }
    else if (msg.type === "typing") {
        typingIndicator.textContent = `${msg.payload.name} yazıyor...`;
        clearTimeout(typingClearTimeout);
        typingClearTimeout = setTimeout(() => {
            typingIndicator.textContent = "";
        }, 3000);
    }
}

function handleSignalingError(err) {
    console.error("WebSocket hatası:", err);
    statusMessage.textContent = "Sunucuya bağlanamadı.Lütfen sunucunun çalıştığından emin olun."
}

function handleSignalingClose() {
    console.log("Bağlantı kapandı");
    if (!intentionalClose) {
        statusMessage.textContent = "Sunucu bağlantısı kesildi.";
    }
}

// Görüşme zaten başladıktan sonra kamera/mikrofon eklendiğinde,
// karşı tarafla yeniden anlaşmak (renegotiation) için yeni bir offer gönderiyoruz.
async function renegotiate() {
    await rtcReady;
    await createAndSendOffer();
}

async function createAndSendOffer() {
    console.log("createAndSendOffer çalışıyor, mevcut sender sayısı:", peerConnection.getSenders().length);

    const offer = await peerConnection.createOffer();
    await peerConnection.setLocalDescription(offer);

    ws.send(JSON.stringify({
        type: "offer",
        room: roomId,
        payload: offer
    }));
}

async function startCamera(withVideo = true, withAudio = true) {
    if (!withVideo && !withAudio) {
        console.log("Kamera/mikrofon istenmeden katılınıyor.");
        return;
    }
    try {
        const stream = await navigator.mediaDevices.getUserMedia({
            video: withVideo,
            audio: withAudio
        });
        localStream = stream;
        localVideo.srcObject = stream;

        let processAudioTrack = null;
        if (stream.getAudioTracks().length > 0) {
            const micSource = audioContext.createMediaStreamSource(stream);
            const destination = audioContext.createMediaStreamDestination();
            micSource.connect(gainNode);
            gainNode.connect(destination);
            processAudioTrack = destination.stream.getAudioTracks()[0];
        }

        await rtcReady;

        for (const track of stream.getTracks()) {
            const trackToSend = track.kind === "audio" ? (processAudioTrack || track) : track;
            const sender = peerConnection.addTrack(trackToSend, stream)
            if (track.kind === "video") {
                videoSender = sender;
                const parameters = sender.getParameters();
                if (!parameters.encodings) {
                    parameters.encodings = [{}]
                }
                parameters.encodings[0].maxBitrate = 500 * 1000
                await sender.setParameters(parameters)
            }
        }
        console.log("Track'ler eklendi");
    } catch (err) {
        console.error("Kamera erişim hatası:", err)
        statusMessage.textContent = "Kamera/mikrofon erişimi reddedildi.Lütfen izin verip sayfayı yenileyin.";
    }
}

let isScreenSharing = false;
let screenStream = null;

toggleScreenBtn.addEventListener("click", async () => {
    if (!isScreenSharing) {

        await startScreenShare();
    } else {
        await stopScreenShare();
    }
})

async function startScreenShare() {
    try {
        screenStream = await navigator.mediaDevices.getDisplayMedia({ media: true })
        const screenTrack = screenStream.getVideoTracks()[0];

        await rtcReady;
        if (videoSender) {
            // Kamera o an kapalıysa (track null) bile aynı sender'ı kullanıyoruz.
            await videoSender.replaceTrack(screenTrack);
        } else {
            videoSender = peerConnection.addTrack(screenTrack);
            await renegotiate();
        }
        localVideo.srcObject = screenStream;
        screenTrack.onended = () => {
            stopScreenShare();
        }
        isScreenSharing = true;
        toggleScreenBtn.textContent = "Paylaşımı durdur";

    } catch (error) {
        console.error("Ekran paylaşımı başlatılamadı:", error)
    }
}

async function stopScreenShare() {
    if (!isScreenSharing) return;

    const cameraTrack = localStream && localStream.getVideoTracks()[0];
    // Kamera kapalıyken ekran paylaşımına başlandıysa, kamera track'i "enabled"
    // durumuna göre geri veriyoruz (kapalıysa null, açıksa gerçek track).
    const trackToRestore = cameraTrack && cameraTrack.enabled ? cameraTrack : null;

    await rtcReady;
    if (videoSender) {
        await videoSender.replaceTrack(trackToRestore);
    }

    if (screenStream) {
        screenStream.getTracks().forEach(t => t.stop());
        screenStream = null;
    }

    localVideo.srcObject = localStream;

    isScreenSharing = false;
    toggleScreenBtn.textContent = "Ekranı Paylaş";
}

let mediaRecorder;
let recordWs;
let isRecording = false;

async function startLiveRecording(stream) {
    const res = await fetch(`/api/record-token?room=${encodeURIComponent(roomId)}`);
    const { token } = await res.json();

    const wsUrl = `${wsProtocol}//${window.location.host}/api/record?token=${encodeURIComponent(token)}`;
    recordWs = new WebSocket(wsUrl);

    recordWs.onopen = () => {
        console.log("Kayıt sunucusuna bağlanıldı.");

        const options = { mimeType: 'video/webm; codecs=vp9' };
        mediaRecorder = new MediaRecorder(stream, options);

        mediaRecorder.ondataavailable = (event) => {
            if (event.data.size > 0 && recordWs.readyState === WebSocket.OPEN) {
                recordWs.send(event.data);
            }
        };

        mediaRecorder.start(2000);

        isRecording = true;
        if (toggleRecordBtn) toggleRecordBtn.textContent = "Kaydı Durdur";
    };

    recordWs.onerror = (err) => console.error("Kayıt WS hatası:", err);
}

function stopLiveRecording() {
    if (mediaRecorder && mediaRecorder.state !== "inactive") {
        mediaRecorder.stop();
    }
    if (recordWs) {
        recordWs.close();
    }
    isRecording = false;
    if (toggleRecordBtn) toggleRecordBtn.textContent = "Kaydı Başlat";
}

function createMergedStream(localStream, remoteStream) {

    const mixerContext = new (window.AudioContext || window.webkitAudioContext)();

    const audioDestination = mixerContext.createMediaStreamDestination();

    if (localStream && localStream.getAudioTracks().length > 0) {
        const localAudioSource = mixerContext.createMediaStreamSource(localStream);
        localAudioSource.connect(audioDestination);
    }

    if (remoteStream && remoteStream.getAudioTracks().length > 0) {
        const remoteAudioSource = mixerContext.createMediaStreamSource(remoteStream);
        remoteAudioSource.connect(audioDestination);
    }

    const mergedAudioTrack = audioDestination.stream.getAudioTracks()[0];


    const canvas = document.createElement("canvas");
    const ctx = canvas.getContext("2d");

    canvas.width = 1280;
    canvas.height = 480;

    function drawFrames() {
        ctx.fillStyle = "black";
        ctx.fillRect(0, 0, canvas.width, canvas.height);

        if (localVideo && localVideo.readyState >= 2) {
            ctx.drawImage(localVideo, 0, 0, 640, 480);
        }

        if (remoteVideo && remoteVideo.readyState >= 2) {
            ctx.drawImage(remoteVideo, 640, 0, 640, 480);
        }

        requestAnimationFrame(drawFrames);
    }

    drawFrames();

    const canvasStream = canvas.captureStream(30);
    const mergedVideoTrack = canvasStream.getVideoTracks()[0];



    return new MediaStream([mergedVideoTrack, mergedAudioTrack]);
}

if (toggleRecordBtn) {
    toggleRecordBtn.addEventListener("click", () => {
        if (!isRecording) {
            const remoteStream = remoteVideo.srcObject;
            let streamToRecord;

            if (remoteStream) {
                console.log("İki taraf da birleştirilerek kaydediliyor...");
                streamToRecord = createMergedStream(localVideo.srcObject, remoteStream);
            } else {
                console.log("Sadece yerel kamera kaydediliyor...");
                streamToRecord = localVideo.srcObject;
            }

            startLiveRecording(streamToRecord);
        } else {
            stopLiveRecording();
        }
    });
}


function formatTime() {
    return new Date().toLocaleTimeString("tr-TR", { hour: "2-digit", minute: "2-digit" });
}

function appendChatMessage(payload, isLocal) {
    const wrapper = document.createElement("div");
    wrapper.style.margin = "6px 0";

    const header = document.createElement("div");
    header.style.fontSize = "11px";
    header.style.color = "#888";
    header.textContent = `${isLocal ? "Sen" : payload.name} • ${formatTime()}`;
    wrapper.appendChild(header);

    if (payload.kind === "file") {
        if (payload.fileType && payload.fileType.startsWith("image/")) {
            const img = document.createElement("img");
            img.src = payload.fileUrl;
            img.style.maxWidth = "160px";
            img.style.maxHeight = "160px";
            img.style.display = "block";
            img.style.cursor = "pointer";
            img.style.borderRadius = "4px";
            img.addEventListener("click", () => window.open(payload.fileUrl, "_blank"));
            wrapper.appendChild(img);
        } else {
            const link = document.createElement("a");
            link.href = payload.fileUrl;
            link.textContent = "📎 " + payload.fileName;
            link.target = "_blank";
            wrapper.appendChild(link);
        }
    } else {
        const textEl = document.createElement("div");
        textEl.textContent = payload.text;
        if (isLocal) textEl.style.color = "#555";
        wrapper.appendChild(textEl);
    }

    chatMessages.appendChild(wrapper);
    chatMessages.scrollTop = chatMessages.scrollHeight;
}

function sendChatMessage() {
    const text = chatInput.value.trim();
    if (!text) return;

    if (ws.readyState !== WebSocket.OPEN) {
        console.warn("WebSocket bağlı değil, mesaj gönderilemedi.");
        return;
    }

    const payload = { kind: "text", text: text, name: myName };

    ws.send(JSON.stringify({
        type: "chat",
        room: roomId,
        payload: payload
    }));

    appendChatMessage(payload, true);
    chatInput.value = "";
}

sendChatBtn.addEventListener("click", sendChatMessage);

chatInput.addEventListener("keydown", (e) => {
    if (e.key === "Enter") {
        sendChatMessage();
    }
});

attachFileBtn.addEventListener("click", () => {
    chatFileInput.click();
});

chatFileInput.addEventListener("change", async () => {
    const file = chatFileInput.files[0];
    if (!file) return;

    if (file.size > 25 * 1024 * 1024) {
        alert("Dosya 25 MB sınırını aşıyor.");
        chatFileInput.value = "";
        return;
    }

    try {
        const tokenRes = await fetch(`/api/record-token?room=${encodeURIComponent(roomId)}`);
        const { token } = await tokenRes.json();

        const formData = new FormData();
        formData.append("file", file);

        const res = await fetch(`/api/upload?token=${encodeURIComponent(token)}`, {
            method: "POST",
            body: formData
        });
        const data = await res.json();

        if (!res.ok) {
            alert(data.error || "Dosya yüklenemedi.");
            return;
        }

        const payload = {
            kind: "file",
            fileUrl: data.url,
            fileName: data.fileName,
            fileType: data.fileType,
            name: myName
        };

        ws.send(JSON.stringify({
            type: "chat",
            room: roomId,
            payload: payload
        }));

        appendChatMessage(payload, true);
    } catch (err) {
        console.error("Dosya yükleme hatası:", err);
        alert("Dosya yüklenirken bir hata oluştu.");
    } finally {
        chatFileInput.value = "";
    }
});

let typingSendTimeout = null;
let typingClearTimeout = null;

chatInput.addEventListener("input", () => {
    if (typingSendTimeout) return;

    if (ws.readyState === WebSocket.OPEN) {
        ws.send(JSON.stringify({
            type: "typing",
            room: roomId,
            payload: { name: myName }
        }));
    }

    typingSendTimeout = setTimeout(() => {
        typingSendTimeout = null;
    }, 1500);
});

setInterval(async () => {
    if (peerConnection && peerConnection.iceConnectionState === "connected") {
        const stats = await peerConnection.getStats();

        stats.forEach(report => {
            if (report.type === 'candidate-pair' && report.state === 'succeeded' && report.nominated) {

                const localCandidate = stats.get(report.localCandidateId);

                if (localCandidate) {
                    const type = localCandidate.candidateType;
                    const connectionType = type === 'relay' ? 'TURN (Sunucu Üzerinden)' : 'STUN (Doğrudan P2P)';

                    const ping = report.currentRoundTripTime * 1000;
                    console.log(`📡 Bağlantı: ${connectionType} | Anlık Ping: ${ping.toFixed(0)} ms`);
                }
            }

            if (report.type === 'outbound-rtp' && report.kind === 'video') {
                if (report.qualityLimitationReason && report.qualityLimitationReason !== "none") {
                    console.warn(`⚠️ Video kalitesi düşürülüyor! Sebep: ${report.qualityLimitationReason}`);
                }
            }

            if (report.type === 'inbound-rtp' && report.kind === 'video') {
                if (report.packetsLost > 0) {
                    console.error(`❌ Ağda ${report.packetsLost} adet video paketi kayboldu!`);
                }
            }
        });
    }
}, 3000);

remoteVolume.addEventListener("input", (e) => {
    remoteGainNode.gain.value = parseFloat(e.target.value);
})

localVolume.addEventListener("input", (e) => {
    gainNode.gain.value = parseFloat(e.target.value);
})