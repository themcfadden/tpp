var selfEasyrtcid = "";
var username = "";

easyrtc.setStreamAcceptor( function(callerEasyrtcid, stream) {
    var video = document.getElementById('callerVideo');
    easyrtc.setVideoObjectSrc(video, stream);
});

easyrtc.setOnStreamClosed( function (callerEasyrtcid) {
    easyrtc.setVideoObjectSrc(document.getElementById('callerVideo'), "");
});

function connect() {
    username = getParameterByName('username');
    console.log("Username:" + username);

    easyrtc.setUsername(username);

    easyrtc.setVideoDims(640,480);
    easyrtc.setRoomOccupantListener(convertListToButtons);

    easyrtc.setAcceptChecker(callAcceptor);

    //easyrtc.easyApp("tpp_webrtc_bot", "selfVideo", ["callerVideo"], loginSuccess, loginFailure);
    easyrtc.initMediaSource(
        function() {
            var selfVideo = document.getElementById("selfVideo");
            easyrtc.setVideoObjectSrc(selfVideo, easyrtc.getLocalStream());
            easyrtc.connect("tpp_webrtc_bot", loginSuccess, loginFailure);
        },
        loginFailure
    );
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
    };
    easyrtc.call(otherEasyrtcid, successCB, failureCB, acceptCB);
}


function callAcceptor(easyrtcid, acceptorCB)
{
    console.log("checking name");
    if(easyrtc.idToName(easyrtcid) == 'matt') {
        acceptorCB(true);
    }
    else {
        acceptorCB(false);
    }
}


function loginSuccess(easyrtcid) {
    selfEasyrtcid = easyrtcid;
    console.log(easyrtcid + " ==> " + easyrtc.idToName(easyrtcid));
    document.getElementById("iam").innerHTML = "I am " + easyrtc.idToName(easyrtcid);
}


function loginFailure(errorCode, message) {
    easyrtc.showError(errorCode, message);
}

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


