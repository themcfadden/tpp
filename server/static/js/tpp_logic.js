var selfEasyrtcid = "";
var username = "";
var gControl =
    {
        username: "",
        hasDataConnection: false,
        joystick1: null,
        joystick2: null
    };


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

    easyrtc.initMediaSource(
        function() {
            var selfVideo = document.getElementById("selfVideo");
            easyrtc.setVideoObjectSrc(selfVideo, easyrtc.getLocalStream());
            easyrtc.connect("tpp_bot", loginSuccess, loginFailure);
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
function virtualJoyStickWorker(xstart, ystart) {
    console.log("touchscreen is", VirtualJoystick.touchScreenAvailable() ? "available" : "not available");
    
    //var joystick1	= new VirtualJoystick({
    gControl.joystick1	= new VirtualJoystick({
        container	: document.getElementById('container1'),
        mouseSupport	: true,
        strokeStyle     : "#888888",
        //limitStickTravel: true,
        limitStickTravel: false,
	stickRadius	: 50,
        stationaryBase  : false,
        //baseX           : xstart,
        //baseY           : ystart,
    });
    
    gControl.joystick2	= new VirtualJoystick({
        container	: document.getElementById('container2'),
        mouseSupport	: true,
        limitStickTravel: true,
	stickRadius	: 50,
        stationaryBase  : false
    });

    //gControl.joystick1.addEventListener('touchStart', function(){
    //    console.log('down');
    //});
    // joystick1.addEventListener('touchEnd', function(){
    //     console.log('up')
    // })

    setInterval(function(){

        // only send data if connected
        if( gControl.hasDataConnection )
        {
            var msg = {'x':gControl.joystick1.deltaX(), 'y':gControl.joystick1.deltaY()};
            sendServerMessage('js1', msg);

            var msg2= {'x':gControl.joystick2.deltaX(), 'y':gControl.joystick2.deltaY()};
            sendServerMessage('js2', msg2);
        }
        
        var output1	= document.getElementById('result1');
        // output1.innerHTML	= '<b>Mouse:</b> '
        //     + ' dx:'+gControl.joystick1.deltaX()
        //     + ' dy:'+gControl.joystick1.deltaY()
        //     + (gControl.joystick1.right()	? ' right'	: '')
        //     + (gControl.joystick1.up()	? ' up'		: '')
        //     + (gControl.joystick1.left()	? ' left'	: '')
        //     + (gControl.joystick1.down()	? ' down' 	: '');
        
        // var output2	= document.getElementById('result2');
        // output2.innerHTML	= '<b>Result2:</b> '
        //     + ' dx:'+joystick2.deltaX()
        //     + ' dy:'+joystick2.deltaY();

    }, 1/30 * 1000);
}

// See http://marcj.github.io/css-element-queries/
function updateSize() {
    var szx = document.getElementById("container1").clientWidth;
    var szy = document.getElementById("container1").clientHeight;
    var res = document.getElementById('result2');
    res.innerHTML = '<b>X,Y: ' + szx + ',' + szy;

}
