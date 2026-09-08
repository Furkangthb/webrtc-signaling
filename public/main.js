const localVideo = document.getElementById("localVideo")
const remoteVideo = document.getElementById("remoteVideo")
const statusMessage = document.getElementById("statusMessage")
const qualityControl = document.getElementById("qualityControl")



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



ws.onmessage = async (event) => {
    const msg = JSON.parse(event.data);
    console.log("Mesaj alındı,tip:", msg.type);

    if (msg.type === "offer") {
        console.log("Offer işleniyor...");
        await peerConnection.setRemoteDescription(msg.payload)

        const answer = await peerConnection.createAnswer();
        await peerConnection.setLocalDescription(answer);

        ws.send(JSON.stringify({
            type: "answer",
            room: roomId,
            payload: answer,
        }));
    } else if (msg.type === "answer") {
        await peerConnection.setRemoteDescription(msg.payload)
    } else if (msg.type === "candidate") {
        try {

            await peerConnection.addIceCandidate(msg.payload)
            console.log("hata yok")
        } catch (error) {
            console.error("hata:catchin içinde")
        }

    }
    else if (msg.type === "ready") {
        console.log("Ready alındı, offer başlatılıyor");
        await cameraReady
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

const peerConnection = new RTCPeerConnection({
    iceServers: [
        { urls: "stun:stun.l.google.com:19302" }
    ]
})

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
    remoteVideo.srcObject = event.streams[0]
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

async function startCamera() {
    try {
        const stream = await navigator.mediaDevices.getUserMedia({
            video: true,
            audio: true
        });

        localVideo.srcObject = stream;

        for (const track of stream.getTracks()) {
            const sender = peerConnection.addTrack(track, stream)
            if (track.kind === "video") {
                const parameters = sender.getParameters();
                if (!parameters.encodings) {
                    parameters.encodings = [{}]

                }
                parameters.encodings[0].maxBitrate=500*1000
                await sender.setParameters(parameters)
            }
        }
        console.log("Track'ler eklendi");
    } catch (err) {
        console.error("Kamerar erişim hatası:", err)
        statusMessage.textContent = "Kamera/mikrofon erişimi reddedildi.Lütfen izin verip sayfayı yenileyin.";
    }
}


setInterval(async () => {
    if (peerConnection && peerConnection.iceConnectionState === "connected") {

        const stats = await peerConnection.getStats();

        stats.forEach(report => {
            if (report.type === 'candidate-pair' && report.state === 'succeeded') {
                const ping = report.currentRoundTripTime * 1000;
                console.log(`📡 Anlık Ping: ${ping.toFixed(0)} ms`);
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

const cameraReady = startCamera();