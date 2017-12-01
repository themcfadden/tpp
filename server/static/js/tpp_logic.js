var selfEasyrtcid = "";
var username = "";
var gControl =
    {
        username: "",
        hasDataConnection: false,
        joystick1: null,
        joystick2: null,
        IAmTheRobot: false,
    };
var mediaStreams = []


easyrtc.setStreamAcceptor( function(callerEasyrtcid, stream) {
    var video = document.getElementById('callerVideo');
    easyrtc.setVideoObjectSrc(video, stream);
});

easyrtc.setOnStreamClosed( function (callerEasyrtcid) {
    easyrtc.setVideoObjectSrc(document.getElementById('callerVideo'), "");
});

function connect() {
    gControl.username = getParameterByName('username');
    console.log("Username:" + gControl.username);

    easyrtc.setUsername(gControl.username);

    easyrtc.setVideoDims(640,480);
    easyrtc.setRoomOccupantListener(convertListToButtons);

    easyrtc.setAcceptChecker(callAcceptor);

    // Set up all media sources
    easyrtc.getVideoSourceList( function(list) {
        for ( i = 0; i < list.length; i++ ) {
            easyrtc.initMediaSource(
                function(mediaStream) {
                    mediaStreams[i] = mediaStream;
                },
                function(errorCode) {
                    console.log("initMedia error callback:" + errorCode);
                },
                "Camera"+i);
        };
    });
                                
    // Init main/default media source
    easyrtc.initMediaSource(
        function() {
            var selfVideo = document.getElementById("selfVideo");
            easyrtc.setVideoObjectSrc(selfVideo, easyrtc.getLocalStream());
            easyrtc.connect("tpp_bot", loginSuccess, loginFailure);
        },
        loginFailure
    );

    // Default to muted local mic, so they don't here me by default.
    muteMe(true);
 }

//function muteMeToggle() {
//    var b = document.getElementById('muteMe');
//    if (b.innerHTML == "Mute Me") {
//        b.innerHTML = "UnMute Me";
//        easyrtc.enableMicrophone(false);
//    }
//    else {
//        b.innerHTML = "Mute Me";
//        easyrtc.enableMicrophone(true);
//    }
//}
//
//function muteThemToggle() {
//    var b = document.getElementById('muteThem');
//    var callerVideo = document.getElementById('callerVideo');
//    if (b.innerHTML == "Mute Them") {
//        b.innerHTML = "UnMute Them";
//        callerVideo.muted = true;
//    }
//    else {
//        b.innerHTML = "Mute Them";
//        callerVideo.muted = false;
//    }
//}

function muteMe(muteFlag) {
    easyrtc.enableMicrophone(muteFlag == false);
    console.log("Microphone Enable set to: ", muteFlag == false);

    var b = document.getElementById('muteMe');
    if (muteFlag) {
        b.innerHTML = "UnMute Me";
    }
    else {
        b.innerHTML = "Mute Me";
    }
}

function muteThem(muteFlag) {
    var callerVideo = document.getElementById('callerVideo');
    callerVideo.muted = muteFlag;

    var b = document.getElementById('muteThem');
    if (muteFlag) {
        b.innerHTML = "UnMute Them";
    }
    else {
        b.innerHTML = "Mute Them";
    }
}

function muteAllToggle() {
    var ma = document.getElementById('muteAll');

    if (ma.innerHTML == "Mute All")  {
        ma.innerHTML = "UnMute All";
        muteMe(true);
        muteThem(true);
    }
    else {
        ma.innerHTML = "Mute All";
        muteMe(false);
        muteThem(false);
    }    
}

function muteMeToggle() {
    var b = document.getElementById('muteMe');
    if (b.innerHTML == "Mute Me") {
        muteMe(true);
        //easyrtc.enableMicrophone(true);
    }
    else {
        muteMe(false);
        //easyrtc.enableMicrophone(false);
    }
}

function muteThemToggle() {
    var b = document.getElementById('muteThem');
    var callerVideo = document.getElementById('callerVideo');
    if (b.innerHTML == "Mute Them") {
        muteThem(true);
    }
    else {
        muteThem(false);
    }
}

function clearConnectList() {
    var otherClientDiv = document.getElementById('otherClients');
    if( otherClientDiv != null )
    {
        while (otherClientDiv.hasChildNodes()) {
            otherClientDiv.removeChild(otherClientDiv.lastChild);
        }
    }
}


function convertListToButtons (roomName, data, isPrimary) {
    clearConnectList();

    var otherClientDiv = document.getElementById('otherClients');
    if( otherClientDiv != null )
    {
        for(var easyrtcid in data) {
            var button = document.createElement('button');
            button.onclick = function(easyrtcid) {
                return function() {
                    performCall(easyrtcid);
                };
            }(easyrtcid);

            var label = document.createTextNode(easyrtc.idToName(easyrtcid));
            button.appendChild(label);
            otherClientDiv.appendChild(button);
        }
    } 
}

function performCall(otherEasyrtcid) {
    easyrtc.hangupAll();

    console.log("calling");

    var successCB = function(easyrtcid) {
        console.log("Successfully called:" + easyrtc.idToName(easyrtcid))
    };
    var failureCB = function() {};
    var acceptCB = function (accepted, easyrtcid) {
        var n = easyrtc.idToName(easyrtcid);
        console.log("acceptCB: " + accepted + " by " + n);
        gControl.hasDataConnection = true;
    };
    easyrtc.call(otherEasyrtcid, successCB, failureCB, acceptCB);
}

function callAcceptor(easyrtcid, acceptorCB)
{
    console.log("checking name");
    if(easyrtc.idToName(easyrtcid) == 'remote') {
        
        acceptorCB(true);
        console.log("I am the robot");
        sendServerMessage('callAccepted', {'who':'remote'});
        gControl.IAmTheRobot = true;
    }
    else {
        acceptorCB(false);
        
    }
}


function loginSuccess(easyrtcid) {
    selfEasyrtcid = easyrtcid;
    console.log(easyrtcid + " ==> " + easyrtc.idToName(easyrtcid));
    document.getElementById("iam").innerHTML = "My Id: " + easyrtc.idToName(easyrtcid);
}

function loginFailure(errorCode, message) {
    easyrtc.showError(errorCode, message);
}

function sendServerMessage(msgType, msgData)
{
    easyrtc.sendServerMessage(msgType, msgData,
                              function(msgType, msgData){
                                  //console.log(msgData);
                              },
                              function(errorCode, errorText){
                                  //console.log(errorText);
                              });
};


//
// Utility Functions
//
function getParameterByName(name, url) {
    if (!url) {
      url = window.location.href;
    }
    name = name.replace(/[\[\]]/g, "\\$&");
    var regex = new RegExp("[?&]" + name + "(=([^&#]*)|&|#|$)"),
        results = regex.exec(url);
    if (!results) return null;
    if (!results[2]) return '';
    return decodeURIComponent(results[2].replace(/\+/g, " "));
}

//
// VirtualJoystick Worker
//
function virtualJoyStickWorker1(xstart, ystart) {
    console.log("touchscreen is", VirtualJoystick.touchScreenAvailable() ? "available" : "not available");
    
    //var joystick1	= new VirtualJoystick({
    // Main Display box on client side.
    gControl.joystick1	= new VirtualJoystick({
        container	: document.getElementById('container1'),
        strokeStyle     : 'green',
        mouseSupport	: true,
        limitStickTravel: true,
        stickRadius	: 100,
        stationaryBase  : true,
        baseX           : xstart,
        baseY           : ystart,
    });
    
    setInterval(function(){
        if( gControl.hasDataConnection )
        {
            var msg = {'x':gControl.joystick1.deltaX(), 'y':gControl.joystick1.deltaY()};
            sendServerMessage('js1', msg);
        }
    }, 1/30 * 1000);
}

function virtualJoyStickWorker2(xstart, ystart) {
    gControl.joystick2	= new VirtualJoystick({
        container	: document.getElementById('container2'),
        strokeStyle     : 'red',
        mouseSupport	: true,
        limitStickTravel: true,
        stickRadius	: 50,
        stationaryBase  : true,
        baseX           : xstart,
        baseY           : ystart,
    });

    setInterval(function(){
        // only send data if connected
        if( gControl.hasDataConnection )
        {
            var msg2= {'x':gControl.joystick2.deltaX(), 'y':gControl.joystick2.deltaY()};
            sendServerMessage('js2', msg2);
        }
    }, 1/30 * 1000);
}

// See http://marcj.github.io/css-element-queries/
function updateSize() {
    var szx = document.getElementById("container1").clientWidth;
    var szy = document.getElementById("container1").clientHeight;
    var res = document.getElementById('result2');
    res.innerHTML = '<b>X,Y: ' + szx + ',' + szy;

}
