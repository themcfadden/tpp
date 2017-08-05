var SerialPort = require("serialport")
var port = new SerialPort("COM25", {
    baudRate:9600,
    dataBits: 8,
    stopBits: 1,
    parity: 'none'});

//var serialCommand = new Buffer(6);
//serialCommand[0] = 0xAA; // Pololu Protocol
//serialCommand[1] = 0x0C; // Device Number
//serialCommand[2] = 0x04; // Set Target Command
//serialCommand[3] = 0x00; // channel
//serialCommand[4] = 0x00; // LSB, 7 bits
//serialCommand[5] = 0x00; // MSB, 7 bits

var serialCommand = Buffer.from([0xAA, 0x0C, 0x04, 0x00, 0x00, 0x00]);


function sleep(milliseconds) {
    var start = new Date().getTime();
  for (var i = 0; i < 1e7; i++) {
    if ((new Date().getTime() - start) > milliseconds){
      break;
    }
  }
}

function writeError(err) {
    if (err) {
        console.log('Error on SerialPort Write: ', err.message);
    }
}

port.on('open', function() {
    console.log('Port is open');

    console.log('1');
    port.write([0x84, 0x00, 0x00, 0x18]);
    port.drain();
    //sleep(1000);

    console.log('2');
    port.write([0x84, 0x00, 0x00, 0x20]);
    port.drain();
    //sleep(1000);

    console.log('3');
    port.write([0x84, 0x00, 0x00, 0x21]);
    port.drain();
    //sleep(1000);

    console.log('4');
    port.write([0x84, 0x00, 0x00, 0x22]);
    port.drain();
    //sleep(1000);

    console.log('6');
    port.write([0x84, 0x00, 0x00, 0x23]);
    port.drain();
    sleep(1000);

    port.write([0x84, 0x00, 0x00, 0x30]);
    port.drain();
    sleep(1000);

//    console.log('7');
//    port.write([0x84, 0x00, 0x00, 0x46]);
//    port.drain();
    sleep(1000);
    
//    console.log('Forward');
//    for ( i = 768; i < 2304; i++ ) {
//        serialCommand[3] = 0x00;
//        serialCommand[4] = ((i*4) & 0x7F);
//        serialCommand[5] = ((i*4) >> 7) & 0x7F;
//
//        // Pololu protocol: 0xAA, device number, 0x04, channel number,
//        //                  target low bits, target high bits
//        //                  0xAA 0x0C 0x04 0x00 0x?? 0x??
//
//        if(i == 2304/2) {
//            console.log("HALF WAY");
//        }
//        
//        console.log(
//            serialCommand[0].toString(16),
//            serialCommand[1].toString(16),
//            serialCommand[2].toString(16),
//            serialCommand[3].toString(16),
//            serialCommand[4].toString(16),
//            serialCommand[5].toString(16));
//        
//        port.write(serialCommand, writeError);
//
//        sleep(10);
//
//////        serialCommand[3] = 0x01;
//////        port.write(serialCommand);
//    }
//    console.log('Back');
//    for ( i = 2304; i > 768; i-- ) {
//        serialCommand[3] = 0x00;
//        serialCommand[4] = ((i*4) & 0x7F);
//        serialCommand[5] = ((i*4) >> 7) & 0x7F;
//        port.write(serialCommand, writeError);
//
//        sleep(1);
//
//    }
    //    console.log("Done");

    //process.exit(0);
});

port.on('error', function(err) {
    console.log('Serial Port Error:', err);
});



        
