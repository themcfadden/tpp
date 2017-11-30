// Load required modules
var https   = require("https");              // http server core module
var fs      = require("fs");
var express = require("express");           // web framework external module
var serveStatic = require('serve-static');  // serve static files
var socketIo = require("socket.io");        // web socket external module
var easyrtc = require("../");               // EasyRTC external module
var SerialPort = require("serialport");
//var serialBuffer = require("CBuffer");
var serialCommand = Buffer.from([0xAA, 0x0C, 0x04, 0x00, 0x00, 0x00]);

// Set process name
process.title = "node-easyrtc";

// Setup and configure Express http server. Expect a subfolder called "static" to be the web root.
var app = express();
app.use(serveStatic('static', {'index': ['index.html']}));

var serialBuffer = [];

// Configure Express http server
const options =
    {
        key:  fs.readFileSync("domain.key"),
        cert: fs.readFileSync("domain.crt")
    };
var webServer = https.createServer(options, app).listen(8443);

// Start Socket.io so it attaches itself to Express server
var socketServer = socketIo.listen(webServer, {"log level":1});

//easyrtc.setOption("logLevel", "debug");
easyrtc.setOption("logLevel", "error");

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


//var comPort = "COM25";
var comPort = "/dev/ttyACM0";
var botSerialPort;
var botSerialPortIsReady = false;
var prevCameraX = 0;
var prevCameraY = 0;
var prevMoveX = 0;
var prevMoveY = 0;

function serialWriteError(err) {
    if (err) {
        console.log('Error on SerialPort Write: ', err.message);
    }
}

function serialWriteData() {
    if ( botSerialPortIsReady )
    {
        while (serialBuffer.length > 0 ){
            //console.log('serialBuffer.length:', serialBuffer.length);
            var xyz = serialBuffer.shift();
            //console.log(xyz[0], xyz[1], xyz[2], xyz[3]);
            botSerialPort.write(xyz);
            botSerialPort.drain();
        }
    }
    else {
        //console.log("No botSerialPort");
    }
}

// Pololu protocol: 0xAA, device number, 0x04, channel number,
//                  target low bits, target high bits
//                  0xAA 0x0C 0x04 0x00 0x?? 0x??
//var serialCommand = Buffer.from([0xAA, 0x0C, 0x04, 0x00, 0x00, 0x00]);

// Compact Protocol: 0x84: Set Target
//                   0xXX: Channel Number
//                   0xXX: 
//var serialCommand = Buffer.from([0x84, 0x00, 0x00, 0x00]);

var multiplier = 1;
var tickCount = 0;

function map(x, in_min, in_max, out_min, out_max) {
  return (x - in_min) * (out_max - out_min) / (in_max - in_min) + out_min;
}

var onEasyrtcMsg = function(connectionObj, msg, socketCallback, next){
    switch(msg.msgType) {
    case 'js1':
        var x = Math.trunc(map(msg.msgData.x, -100, 100, 1435, 1470));
        var y = Math.trunc(map(msg.msgData.y, -100, 100, 1470, 1435));

        if ( ( x != prevCameraX ) || (y != prevCameraY))
        {
            var cmdX = [0x84];
            cmdX.push(0);
            cmdX.push(((x * multiplier) >> 7) & 0x7F);
            cmdX.push((x * multiplier) & 0x7F);
            serialBuffer.push(cmdX);


            var cmdY = [0x84];
            cmdY.push(1);
            cmdY.push(((y * multiplier) >> 7) & 0x7F);
            cmdY.push((y * multiplier) & 0x7F);
            serialBuffer.push(cmdY);
            
            //console.log('Camera:',
            //            'X:',x, 'Y:', y,
            //            'Cmd:', cmdX[2], cmdX[3], cmdY[2], cmdY[3]);

            prevCameraX = x;
            prevCameraY = y;

        }
        socketCallback({msgType:'js1', msgData:'done1'});
        next(null);
        serialWriteData();
        break;
    case 'js2':
        var x = Math.trunc(map(msg.msgData.x, -50, 50, 1430, 1470));
        var y = Math.trunc(map(msg.msgData.y, -50, 50, 1470, 1430));

        if ( ( x != prevMoveX ) || (y != prevMoveY))
        {
            var cmdX = [0x84];
            cmdX.push(2); // channel
            cmdX.push(((x * multiplier) >> 7) & 0x7F);
            cmdX.push((x * multiplier) & 0x7F);
            serialBuffer.push(cmdX);

            var cmdY = [0x84];
            cmdY.push(3); // channel
            cmdY.push(((y * multiplier) >> 7) & 0x7F);
            cmdY.push((y * multiplier) & 0x7F);
            serialBuffer.push(cmdY);
            
            console.log('Move:',
                        'X:',x, 'Y:', y,
                        'Cmd:', cmdX[2], cmdX[3], cmdY[2], cmdY[3]);
            prevMoveX = x;
            prevMoveY = y;
        }
        socketCallback({msgType:'js2', msgData:'done2'});
        next(null);
        serialWriteData();
        break;

    case 'callAccepted':
        // Add serial port set up here
        if (botSerialPort != null )
        {
            console.log("botSerialPort is NOT NULL");
            botSerialPort.close();
        }
        botSerialPort = new SerialPort(comPort,
                                       {
                                           baudRate:9600,
                                           dataBits: 8,
                                           stopBits: 1,
                                           parity: 'none'
                                       });
        
        //console.log('botSerialPort:', botSerialPort);
                                           
        botSerialPort.on('open', function() {
            console.log("Serial Port Open");
            botSerialPortIsReady = true;
        });

        botSerialPort.on('error', function(err) {
            console.log('Serial Port Error:', err);
            botSerialPortIsReady = false;
            console.log(botSerialPort);
        });

        // Set servo accel
        var cmdServo = [0x89];
        cmdServo.push(0);          // channel
        cmdServo.push(0x3 & 0x7F); // low bits
        //cmdServo.push(0 & 0x7F); // low bits
        cmdServo.push(0);          // high bits
        serialBuffer.push(cmdServo);
        serialWriteData();
        
        cmdServo = [0x89];
        cmdServo.push(1);          // channel
        cmdServo.push(0x3 & 0x7F); // low bits
        //cmdServo.push(0 & 0x7F); // low bits
        cmdServo.push(0);          // high bits
        serialBuffer.push(cmdServo);
        serialWriteData();

        console.log('Got a callAccepted', msg.msgData);
        if( msg.msgData.who == 'remote') {
            console.log('Remote (the Robot) Connected', msg.msgData);
        }
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
