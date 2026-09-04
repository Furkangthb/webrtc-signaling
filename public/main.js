const localVideo= document.getElementById("localVideo")
const remoteVideo=document.getElementById("remoteVideo")

const roomId="oda1";
const ws= new WebSocket("ws:/localhost:8080/ws")

ws.onopen=()=>{
    console.log("Signaling server \'a bağlanıldı");
    ws.send(JSON.stringify({type:"join",room:roomId,payload:null}));
}

ws.onmessage=(event)=>{
    const msg =JSON.parse(event.data);
    console.log("Mesaj geldi:",msg)
};
ws.onerror=(err)=>{
    console.error("WebSocket hatası:",err);
};

ws.onclose=()=>{
    console.log("Bağlantı kapandı");
};

const peerConnection =new RTCPeerConnection({
    iceServers:[
        {urls:"stun:stun.1.google.com:19302"}
    ]
})

async function startCamera() {
    try {
        const stream=await navigator.mediaDevices.getUserMedia({
            video:true,
            audio:true
        });
        localVideo.srcObject=stream;
    } catch (err) {
        console.error("Kamerar erişim hatası:",err)
    }
}

startCamera();