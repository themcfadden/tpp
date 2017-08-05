// Load required modules
var https   = require("https");              // http server core module
var fs      = require("fs");
var express = require("express");           // web framework external module
var serveStatic = require('serve-static');  // serve static files
var socketIo = require("socket.io");        // web socket external module
var easyrtc = require("../");               // EasyRTC external module
var SerialPort = require("serialport")
var serialCommand = Buffer.from([0xAA, 0x0C, 0x04, 0x00, 0x00, 0x00]);


// Set process name
process.title = "node-easyrtc";

// Setup and configure Express http server. Expect a subfolder called "static" to be the web root.
var app = express();
app.use(serveStatic('static', {'index': ['index.html']}));

// Start Express http server on port 8080
//var webServer = http.createServer(app).listen(8080);
const options =
    {
        key:  fs.readFileSync("domain.key"),
        cert: fs.readFileSync("domain.crt")
    };

var webServer = https.createServer(options, app).listen(8443);


// Start Socket.io so it attaches itself to Express server
var socketServer = socketIo.listen(webServer, {"log level":1});

easyrtc.setOption("logLevel", "debug");

// Overriding the default easyrtcAuth listener, only so we can directly access its callback
easyrtc.events.on("easyrtcAuth", function(socket, easyrtcid, msg, socketCallback, callback) {
    easyrtc.events.defaultListeners.easyrtcAuth(socket, easyrtcid, msg, socketCallback, function(err, connectionObj){
        if (err || !msg.msgData || !msg.msgData.credential || !connectionObj) {
            callback(err, connectionObj);
            return;
        }

        connectionObj.setField("credential", msg.msgData.credential, {"isShared":false});

        console.log("["+easyrtcid+"] Credential saved!", connectionObj.getFieldValueSync("credential"));

        callback(err, connectionObj);
    });
});

// To test, lets print the credential to the console for every room join!
easyrtc.events.on("roomJoin", function(connectionObj, roomName, roomParameter, callback) {
    console.log("["+connectionObj.getEasyrtcid()+"] Credential retrieved!", connectionObj.getFieldValueSync("credential"));
    easyrtc.events.defaultListeners.roomJoin(connectionObj, roomName, roomParameter, callback);
});

// Start EasyRTC server
var rtc = easyrtc.listen(app, socketServer, null, function(err, rtcRef) {
    console.log("Initiated");

    rtcRef.events.on("roomCreate", function(appObj, creatorConnectionObj, roomName, roomOptions, callback) {
        console.log("roomCreate fired! Trying to create: " + roomName);
        appObj.events.defaultListeners.roomCreate(appObj, creatorConnectionObj, roomName, roomOptions, callback);
    });
});


var comPort = "COM25";
var botSerialPort;
var prevCameraX = 0;
var prevCameraY = 0;
var prevMoveX = 0;
var prevMoveY = 0;

function serialWriteError(err) {
    if (err) {
        console.log('Error on SerialPort Write: ', err.message);
    }
}

// Pololu protocol: 0xAA, device number, 0x04, channel number,
//                  target low bits, target high bits
//                  0xAA 0x0C 0x04 0x00 0x?? 0x??
//var serialCommand = Buffer.from([0xAA, 0x0C, 0x04, 0x00, 0x00, 0x00]);

// Compact Protocol: 0x84: Set Target
//                   0xXX: Channel Number
//                   0xXX: 
var serialCommand = Buffer.from([0x84, 0x00, 0x00, 0x00]);

var multiplier = 10;

var onEasyrtcMsg = function(connectionObj, msg, socketCallback, next){
    switch(msg.msgType) {
    case 'js1':
        if ( ( msg.msgData.x != prevCameraX ) || (msg.msgData.y != prevCameraY))
        {
            serialCommand[1] = 0;
            serialCommand[2] = ((prevCameraX + msg.msgData.x) * multiplier)& 0x7F;
            serialCommand[3] = (((prevCameraX + msg.msgData.x) * multiplier) >> 7) & 0x7F;
            botSerialPort.write(serialCommand, serialWriteError);

            console.log('Camera:', msg.msgData, msg.msgData.x,serialCommand[2], serialCommand[3]);

            serialCommand[1] = 1;
            serialCommand[2] = (msg.msgData.y * multiplier) & 0x7F;
            serialCommand[3] = ((msg.msgData.y * multiplier) >> 7) & 0x7F;
            botSerialPort.write(serialCommand, serialWriteError);

            prevCameraX = msg.msgData.x;
            prevCameraY = msg.msgData.y;

        }
        socketCallback({msgType:'js1', msgData:'done1'});
        next(null);
        break;
    case 'js2':
        if ( ( msg.msgData.x != prevMoveX ) || (msg.msgData.y != prevMoveY))
        {
            console.log('Move:', msg.msgData);
            prevMoveX = msg.msgData.x;
            prevMoveY = msg.msgData.y;
        }
        socketCallback({msgType:'js2', msgData:'done2'});
        next(null);
        break;
    case 'callAccepted':
        // Add serial port set up here
        botSerialPort = new SerialPort(comPort,
                                       {
                                           baudRate:9600,
                                           dataBits: 8,
                                           stopBits: 1,
                                           parity: 'none'
                                       });
        console.log('botSerialPort:', botSerialPort);
                                           
        botSerialPort.on('open', function() {
            console.log("Serail Port Open");
        });

        botSerialPort.on('error', function(err) {
            console.log('Serila Port Error:', err.message);
        });


        console.log('Got a callAccepted', msg.msgData);
        if( msg.msgData.who == 'remote')
            console.log('Remote (the Robot) Connected', msg.msgData);
        socketCallback({msgType:'callAccepted', msgData:''});
        next(null);
        break;
    default:
        easyrtc.events.emitDefault("easyrtcMsg", connectionObj, msg, socketCallback, next);
        break;
    }
};

easyrtc.events.on("easyrtcMsg", onEasyrtcMsg);

function successCB(msgType, msgDate) {
    console.log("MattMc-- Got success msg" + msgType + msgData);
}

function failCB(msgType, msgDate) {
    console.log("MattMc-- Got fail msg" + msgType + msgData);
}

webServer.listen(8433, function () {
    console.log('listening on http://localhost:8443');
});
