# Smart Laundry System

## Users

- Admin
- Laundromat
- Handler
- Student
- Warden

## Admin

Admin sets up and manages the whole system

- Create/manage laundromats
- Create/manage hostels
- Create/manage students

## Laundromat

The laundromat manages pickups and deliveries from their device

### Pickup

- Create a pickup for a hostel
- Assign a handler to it
- See scheduled pickups
- See which bags were picked up in a pickup

### Pickup Batch

Every pickup creates a pickup batch

When a handler scans a student's bag QR code, that bag gets added to the batch

The batch should keep track of:

- Hostel
- Pickup date and time
- Bags collected

### Delivery

- See bags that have already been picked up
- Filter them by pickup batch
- Select bags for delivery
- Schedule a delivery
- Assign a handler
- See scheduled deliveries

## Handler

Handler handles the physical pickup and delivery

### Pickup

- See pickups assigned to them
- Start a pickup
- Scan bag QR codes
- Pick up the scanned bags
- Complete the pickup

Every scanned bag gets added to that pickup's batch

### Delivery

- See deliveries assigned to them
- Start a delivery
- Scan the bag QR code
- Deliver the bag to the student
- Complete the delivery

### Failed Delivery

If the student isn't available/doesn't collect the bag:

1. Bag's delivery is marked as failed
2. Handler takes the bag back to the laundromat
3. Bag becomes available for a future delivery

## Student

Student can:

- Get notified when their pickup is scheduled
- Get notified when their delivery is scheduled
- See the status of their current pickup/delivery
- See their laundry history

Each bag has a QR code that identifies the student bag during pickup and delivery

## Warden / Hostel Device

Each hostel has a device that wardens can use

Warden can:

- Register students belonging to that hostel
- Remove students belonging to that hostel

Basically, the warden's device is another way to manage students without needing the admin

## Laundry History

Keep a history for each student

It should contain their previous:

- Pickups
- Deliveries, both failed and successful

## Basic Flow

### Pickup

1. Laundromat creates a pickup for a hostel
2. Laundromat assigns a handler
3. Handler starts the pickup
4. Handler scans each bag's QR code
5. Scanned bags are added to the pickup batch
6. Handler completes the pickup

### Delivery

1. Laundromat looks at available picked-up bags
2. Bags can be filtered by pickup batch
3. Laundromat selects the bags to deliver
4. Laundromat creates the delivery and assigns a handler
5. Students whose bags are included get notified
6. Handler starts the delivery
7. Handler scans each bag
8. Handler delivers the bag to the student
9. Handler completes the delivery

### Failed Delivery

1. Handler tries to deliver the bag
2. Student doesn't collect it
3. Handler takes the bag back to the laundromat
4. Bag becomes available for another delivery
