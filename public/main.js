const localVideo = document.getElementById("localVideo")
const remoteVideo = document.getElementById("remoteVideo")
const statusMessage = document.getElementById("statusMessage")
const qualityControl = document.getElementById("qualityControl")
const toggleMicBtn = document.getElementById("toggleMicBtn")
const toggleVideoBtn = document.getElementById("toggleVideoBtn")
const remoteVolume = document.getElementById("remoteVolume")
const localVolume = document.getElementById("localVolume")
const toggleScreenBtn = document.getElementById("toggleScreenBtn")
const toggleRecordBtn = document.getElementById("toggleRecordBtn");


let localStream = null;

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

        let iceServers = [{ urls: "stun:furkanturn.duckdns.org:3478" }];

        if (data.success) {
            console.log("TURN bilgileri başarıyla alındı.");
            iceServers.push({
                urls: [
                    "turn:furkanturn.duckdns.org:3478?transport=udp",
                    "turn:furkanturn.duckdns.org:3478?transport=tcp",
                    "turns:furkanturn.duckdns.org:5349?transport=tcp"
                ],
                username: data.username,
                credential: data.credential
            });
        } else {
            console.warn("TURN bilgileri alınamadı, sadece STUN ile devam ediliyor.");
        }

        peerConnection = new RTCPeerConnection({ iceServers: iceServers });

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
        peerConnection = new RTCPeerConnection({ iceServers: [{ urls: "stun:furkanturn.duckdns.org:3478" }] });
    }
}

const rtcReady = initializeWebRTC();
// ==========================================

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

toggleVideoBtn.addEventListener("click", () => {
    const stream = localVideo.srcObject;
    if (stream) {
        const videoTrack = stream.getVideoTracks()[0]
        if (videoTrack) {
            videoTrack.enabled = !videoTrack.enabled
            if (videoTrack.enabled) {
                toggleVideoBtn.textContent = "Kamerayı kapat";
            }
            else {
                toggleVideoBtn.textContent = "Kamerayı aç";
            }
        }
    }
})

toggleMicBtn.addEventListener("click", () => {
    const stream = localVideo.srcObject;
    if (stream) {
        const audioTrack = stream.getAudioTracks()[0]
        if (audioTrack) {
            audioTrack.enabled = !audioTrack.enabled
            if (audioTrack.enabled) {
                toggleMicBtn.textContent = "Mikrofunu kapat";
            }
            else {
                toggleMicBtn.textContent = "Mikrofonu aç";
            }
        }
    }
})

const urlParams = new URLSearchParams(window.location.search)
let roomId = urlParams.get("room")

if (!roomId) {
    roomId = crypto.randomUUID();
    window.location.search = `?room=${roomId}`;
}

const ws = new WebSocket("wss://webrtc-signaling-kjw9.onrender.com/ws")
ws.onopen = () => {
    console.log("Signaling server \'a bağlanıldı");
    ws.send(JSON.stringify({ type: "join", room: roomId, payload: null }));
}

window.addEventListener('beforeunload', function () {
    if (ws && ws.readyState === WebSocket.OPEN) {
        ws.close();
    }
});

ws.onmessage = async (event) => {
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
};

ws.onerror = (err) => {
    console.error("WebSocket hatası:", err);
    statusMessage.textContent = "Sunucuya bağlanamadı.Lütfen sunucunun çalıştığından emin olun."
};

let intentionalClose = false;

ws.onclose = () => {
    console.log("Bağlantı kapandı");
    if (!intentionalClose) {
        statusMessage.textContent = "Sunucu bağlantısı kesildi.";
    }
};

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

async function startCamera() {
    try {
        const stream = await navigator.mediaDevices.getUserMedia({
            video: true,
            audio: true
        });
        localStream = stream;
        localVideo.srcObject = stream;

        const micSource = audioContext.createMediaStreamSource(stream);
        const destination = audioContext.createMediaStreamDestination();
        micSource.connect(gainNode);
        gainNode.connect(destination);
        const processAudioTrack = destination.stream.getAudioTracks()[0];

        await rtcReady; 

        for (const track of stream.getTracks()) {
            const trackToSend = track.kind === "audio" ? processAudioTrack : track;
            const sender = peerConnection.addTrack(trackToSend, stream)
            if (track.kind === "video") {
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

const cameraReady = startCamera(); 

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
        const videoSender = peerConnection.getSenders().find(s => s.track && s.track.kind === "video")
        if (videoSender) {
            await videoSender.replaceTrack(screenTrack);
        }
        localVideo.srcObject = screenStream;
        screenTrack.onended = () => {
            stopScreenShare();
        }
        isScreenSharing = true;
        toggleScreenBtn.textContent = "Paylaşımı durdur";

    } catch (error) {
        console.error("Ekran paylaşımı başlatılamadı:",error)
    }
}

async function stopScreenShare(){
    if(!isScreenSharing) return;

    const cameraTrack = localStream.getVideoTracks()[0]
    
    await rtcReady;
    const videoSender = peerConnection.getSenders().find(
        s => s.track && s.track.kind === "video"
    )

    if (videoSender && cameraTrack) {
        await videoSender.replaceTrack(cameraTrack);
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

const recordWsUrl = "wss://furkanturn.duckdns.org/api/record";

function startLiveRecording(stream) {
    recordWs = new WebSocket(recordWsUrl);
    
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
        if(toggleRecordBtn) toggleRecordBtn.textContent = "Kaydı Durdur";
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
    if(toggleRecordBtn) toggleRecordBtn.textContent = "Kaydı Başlat";
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