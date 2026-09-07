const localVideo = document.getElementById("localVideo")
const remoteVideo = document.getElementById("remoteVideo")
const statusMessage=document.getElementById("statusMessage")


const roomId = "oda1";
const ws = new WebSocket("ws://localhost:8080/ws")

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

};
ws.onerror = (err) => {
    console.error("WebSocket hatası:", err);
    statusMessage.textContent="Sunucuya bağlanamadı.Lütfen sunucunun çalıştığından emin olun."
};

ws.onclose = () => {
    console.log("Bağlantı kapandı");
    statusMessage.textContent="Sunucu bağlantısı kesildi."
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

        stream.getTracks().forEach((track) => {
            peerConnection.addTrack(track, stream)
        })
        console.log("Track'ler eklendi");
    } catch (err) {
        console.error("Kamerar erişim hatası:", err)
        statusMessage.textContent="Kamera/mikrofon erişimi reddedildi.Lütfen izin verip sayfayı yenileyin.";
    }
}

const cameraReady = startCamera();